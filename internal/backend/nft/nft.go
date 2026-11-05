// Package nft enforces the network plan with a dedicated nftables table
// (`inet blockd`). It never touches other tables, so it coexists with ufw.
package nft

import (
	"context"
	"encoding/json"
	"fmt"
	"net/netip"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/carlos-frey/blockd/internal/backend"
	"github.com/carlos-frey/blockd/internal/backend/sysexec"
	"github.com/carlos-frey/blockd/internal/domain"
)

const (
	Family = "inet"
	Table  = "blockd"
)

type Blocker struct {
	Run sysexec.Runner
	// JailPath is the jail cgroup relative to the cgroup2 root, e.g. "blockd.jail".
	JailPath string
	// Bin is the nft binary.
	Bin string
}

func New(jailPath string) *Blocker {
	return &Blocker{Run: sysexec.Exec{}, JailPath: jailPath, Bin: "nft"}
}

var _ backend.NetworkBlocker = (*Blocker)(nil)

func (b *Blocker) Apply(ctx context.Context, plan backend.NetPlan) error {
	_, err := b.Run.Run(ctx, []byte(Ruleset(plan, b.JailPath)), b.Bin, "-f", "-")
	return err
}

func (b *Blocker) Teardown(ctx context.Context) error {
	return b.Apply(ctx, backend.NetPlan{})
}

func (b *Blocker) SetDynamic(ctx context.Context, addrs []netip.Addr) error {
	var v4, v6 []string
	for _, a := range addrs {
		if a.Unmap().Is4() {
			v4 = append(v4, a.Unmap().String())
		} else {
			v6 = append(v6, a.String())
		}
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "flush set %s %s dyn4\nflush set %s %s dyn6\n", Family, Table, Family, Table)
	if len(v4) > 0 {
		fmt.Fprintf(&sb, "add element %s %s dyn4 { %s }\n", Family, Table, strings.Join(v4, ", "))
	}
	if len(v6) > 0 {
		fmt.Fprintf(&sb, "add element %s %s dyn6 { %s }\n", Family, Table, strings.Join(v6, ", "))
	}
	_, err := b.Run.Run(ctx, []byte(sb.String()), b.Bin, "-f", "-")
	return err
}

var commentRe = regexp.MustCompile(`^blockd fp=([0-9a-f]+) rules=(\d+)$`)

// Fingerprint reads the table back. The comment carries the fingerprint and
// the number of rules we installed in the output chain; a different count
// means someone edited the table, which is reported as "" (drift).
func (b *Blocker) Fingerprint(ctx context.Context) (string, error) {
	out, err := b.Run.Run(ctx, nil, b.Bin, "-j", "list", "table", Family, Table)
	if err != nil {
		if strings.Contains(err.Error(), "No such file or directory") {
			return "", nil
		}
		return "", err
	}
	var doc struct {
		Nftables []map[string]json.RawMessage `json:"nftables"`
	}
	if err := json.Unmarshal(out, &doc); err != nil {
		return "", fmt.Errorf("nft -j: %w", err)
	}
	var comment string
	rules := 0
	for _, obj := range doc.Nftables {
		if raw, ok := obj["table"]; ok {
			var t struct {
				Comment string `json:"comment"`
			}
			_ = json.Unmarshal(raw, &t)
			comment = t.Comment
		}
		if raw, ok := obj["rule"]; ok {
			var r struct {
				Chain string `json:"chain"`
			}
			_ = json.Unmarshal(raw, &r)
			if r.Chain == "output" {
				rules++
			}
		}
	}
	m := commentRe.FindStringSubmatch(comment)
	if m == nil {
		return "", nil
	}
	if want, _ := strconv.Atoi(m[2]); want != rules {
		return "", nil
	}
	return m[1], nil
}

// Ruleset renders the full, atomic nft script for a plan. The leading
// "table + delete table" pair makes it idempotent whether or not the table
// exists; an empty plan only deletes it.
func Ruleset(plan backend.NetPlan, jailPath string) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "table %s %s\ndelete table %s %s\n", Family, Table, Family, Table)
	if plan.Empty() {
		return sb.String()
	}

	var rules []string
	if plan.JailProcesses {
		level := strings.Count(strings.Trim(jailPath, "/"), "/") + 1
		rules = append(rules, fmt.Sprintf("socket cgroupv2 level %d %q jump deny", level, strings.Trim(jailPath, "/")))
	}
	if plan.Dynamic {
		rules = append(rules, "ip daddr @dyn4 jump deny", "ip6 daddr @dyn6 jump deny")
	}
	rules = append(rules, staticRules(plan.Static)...)

	fmt.Fprintf(&sb, "table %s %s {\n", Family, Table)
	fmt.Fprintf(&sb, "\tcomment \"blockd fp=%s rules=%d\"\n", backend.Fingerprint(plan), len(rules))
	if plan.Dynamic {
		sb.WriteString("\tset dyn4 { type ipv4_addr; flags interval; auto-merge; }\n")
		sb.WriteString("\tset dyn6 { type ipv6_addr; flags interval; auto-merge; }\n")
	}
	// TCP gets a reset so connects fail instantly; the rest an ICMP error.
	sb.WriteString("\tchain deny {\n")
	sb.WriteString("\t\tmeta l4proto tcp reject with tcp reset\n")
	sb.WriteString("\t\treject with icmpx admin-prohibited\n")
	sb.WriteString("\t}\n")
	sb.WriteString("\tchain output {\n")
	sb.WriteString("\t\ttype filter hook output priority filter - 1; policy accept;\n")
	for _, r := range rules {
		fmt.Fprintf(&sb, "\t\t%s\n", r)
	}
	sb.WriteString("\t}\n}\n")
	return sb.String()
}

// staticRules groups fixed prefixes by family and port list, one rule each.
func staticRules(static []domain.IPRule) []string {
	type key struct {
		v6    bool
		ports string
	}
	groups := map[key][]string{}
	var keys []key
	for _, r := range static {
		ports := make([]string, len(r.Ports))
		for i, p := range r.Ports {
			ports[i] = strconv.Itoa(int(p))
		}
		k := key{v6: r.Prefix.Addr().Is6() && !r.Prefix.Addr().Is4In6(), ports: strings.Join(ports, ", ")}
		if _, ok := groups[k]; !ok {
			keys = append(keys, k)
		}
		addr := r.Prefix.String()
		if r.Prefix.IsSingleIP() {
			addr = r.Prefix.Addr().String()
		}
		groups[k] = append(groups[k], addr)
	}
	slices.SortFunc(keys, func(a, b key) int {
		if a.v6 != b.v6 {
			if a.v6 {
				return 1
			}
			return -1
		}
		return strings.Compare(a.ports, b.ports)
	})
	var out []string
	for _, k := range keys {
		fam := "ip"
		if k.v6 {
			fam = "ip6"
		}
		r := fmt.Sprintf("%s daddr { %s }", fam, strings.Join(groups[k], ", "))
		if k.ports != "" {
			r += fmt.Sprintf(" meta l4proto { tcp, udp } th dport { %s }", k.ports)
		}
		out = append(out, r+" jump deny")
	}
	return out
}
