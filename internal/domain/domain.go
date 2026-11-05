// Package domain holds the pure model of blockd: catalog apps, profiles, the
// persisted state and the resolution of "what is blocked and why".
package domain

import (
	"fmt"
	"net/netip"
	"regexp"
	"slices"
	"strings"
)

// Domain is a hostname to block. The block always covers subdomains.
// IPBlock opts the domain into IP-level blocking: its resolved addresses are
// also dropped by the firewall. It is off by default because big providers
// (Meta, Google) share edge IPs across services and would be overblocked.
type Domain struct {
	Name    string `json:"name" yaml:"name"`
	IPBlock bool   `json:"ip_block,omitempty" yaml:"ip_block,omitempty"`
}

// UnmarshalYAML accepts both a plain string and the full mapping.
func (d *Domain) UnmarshalYAML(unmarshal func(any) error) error {
	var s string
	if err := unmarshal(&s); err == nil {
		d.Name = s
		return nil
	}
	type raw Domain
	return unmarshal((*raw)(d))
}

// App is a catalog entry: anything that can be blocked.
type App struct {
	ID          string   `json:"id" yaml:"id"`
	Name        string   `json:"name" yaml:"name"`
	Category    string   `json:"category" yaml:"category"`
	Description string   `json:"description,omitempty" yaml:"description,omitempty"`
	Domains     []Domain `json:"domains,omitempty" yaml:"domains,omitempty"`
	// Processes are executable names (comm or exe basename) whose network is cut.
	Processes []string `json:"processes,omitempty" yaml:"processes,omitempty"`
	// IPs are fixed addresses or CIDRs, blocked on Ports (all ports when empty).
	IPs     []string `json:"ips,omitempty" yaml:"ips,omitempty"`
	Ports   []uint16 `json:"ports,omitempty" yaml:"ports,omitempty"`
	Builtin bool     `json:"builtin" yaml:"-"`
}

// Profile is a named group of apps.
type Profile struct {
	ID          string   `json:"id" yaml:"id"`
	Name        string   `json:"name" yaml:"name"`
	Description string   `json:"description,omitempty" yaml:"description,omitempty"`
	Apps        []string `json:"apps" yaml:"apps"`
	Builtin     bool     `json:"builtin" yaml:"-"`
}

// State is everything the user changes; it is what gets persisted.
type State struct {
	Version        int            `json:"version"`
	ActiveProfiles []string       `json:"active_profiles"`
	ManualApps     []string       `json:"manual_apps"`
	CustomApps     []App          `json:"custom_apps"`
	UserProfiles   []Profile      `json:"user_profiles"`
	Schedules      []Schedule     `json:"schedules"`
	Focus          []FocusSession `json:"focus"`
	Pauses         []Pause        `json:"pauses"`
}

// StateVersion 2 added schedules, focus sessions and pauses; a version 1 file
// loads as is.
const StateVersion = 2

// ReasonManual marks an app blocked individually, outside any profile.
const ReasonManual = "manual"

// ProfileReason is the BlockedBy entry for an app blocked by a profile that
// was switched on by hand. Schedules and focus sessions use their Source id.
func ProfileReason(id string) string { return "profile:" + id }

var idRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

// ValidID reports whether s is usable as an app or profile id.
func ValidID(s string) bool { return idRe.MatchString(s) }

// Slugify turns a display name into an id ("Redes Sociais" -> "redes-sociais").
func Slugify(name string) string {
	repl := strings.NewReplacer(
		"á", "a", "à", "a", "â", "a", "ã", "a", "ä", "a",
		"é", "e", "ê", "e", "è", "e", "í", "i", "ì", "i",
		"ó", "o", "ô", "o", "õ", "o", "ö", "o", "ú", "u", "ü", "u",
		"ç", "c", "ñ", "n", "&", "e",
	)
	s := repl.Replace(strings.ToLower(strings.TrimSpace(name)))
	var b strings.Builder
	dash := false
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			dash = false
		case b.Len() > 0 && !dash:
			b.WriteByte('-')
			dash = true
		}
	}
	out := strings.TrimSuffix(b.String(), "-")
	if len(out) > 63 {
		out = strings.TrimSuffix(out[:63], "-")
	}
	return out
}

// Validate checks an app definition, normalizing it in place.
func (a *App) Validate() error {
	if !ValidID(a.ID) {
		return Errorf(CodeInvalidArgument, "id de app inválido %q (use a-z, 0-9 e -)", a.ID)
	}
	a.Name = strings.TrimSpace(a.Name)
	if a.Name == "" {
		return Errorf(CodeInvalidArgument, "app %q precisa de um nome", a.ID)
	}
	if a.Category == "" {
		a.Category = "Outros"
	}
	for i := range a.Domains {
		d := strings.ToLower(strings.TrimSpace(a.Domains[i].Name))
		d = strings.TrimPrefix(strings.TrimPrefix(d, "*."), ".")
		if d == "" || strings.ContainsAny(d, " /:#") || !strings.Contains(d, ".") {
			return Errorf(CodeInvalidArgument, "domínio inválido %q em %s", a.Domains[i].Name, a.ID)
		}
		a.Domains[i].Name = d
	}
	for _, p := range a.Processes {
		if p == "" || strings.ContainsAny(p, "/ ") {
			return Errorf(CodeInvalidArgument, "nome de processo inválido %q em %s", p, a.ID)
		}
	}
	for _, ip := range a.IPs {
		if _, err := ParsePrefix(ip); err != nil {
			return Errorf(CodeInvalidArgument, "IP inválido %q em %s", ip, a.ID)
		}
	}
	if len(a.Domains)+len(a.Processes)+len(a.IPs) == 0 {
		return Errorf(CodeInvalidArgument, "app %q precisa de pelo menos um domínio, processo ou IP", a.ID)
	}
	return nil
}

// ParsePrefix accepts a bare address or a CIDR.
func ParsePrefix(s string) (netip.Prefix, error) {
	if strings.Contains(s, "/") {
		p, err := netip.ParsePrefix(s)
		return p.Masked(), err
	}
	a, err := netip.ParseAddr(s)
	if err != nil {
		return netip.Prefix{}, err
	}
	return netip.PrefixFrom(a, a.BitLen()), nil
}

// Normalize sorts and dedups the set-like fields so state files are stable.
func (s *State) Normalize() {
	s.Version = StateVersion
	s.ActiveProfiles = sortedSet(s.ActiveProfiles)
	s.ManualApps = sortedSet(s.ManualApps)
	if s.CustomApps == nil {
		s.CustomApps = []App{}
	}
	if s.UserProfiles == nil {
		s.UserProfiles = []Profile{}
	}
	if s.Schedules == nil {
		s.Schedules = []Schedule{}
	}
	for i := range s.Schedules {
		s.Schedules[i].Profiles = sortedSet(s.Schedules[i].Profiles)
		s.Schedules[i].Skip = sortedSet(s.Schedules[i].Skip)
	}
	if s.Focus == nil {
		s.Focus = []FocusSession{}
	}
	sortFocus(s.Focus)
	if s.Pauses == nil {
		s.Pauses = []Pause{}
	}
}

func sortedSet(in []string) []string {
	out := slices.Clone(in)
	slices.Sort(out)
	out = slices.Compact(out)
	if out == nil {
		out = []string{}
	}
	return out
}

func (p Profile) String() string { return fmt.Sprintf("%s (%d apps)", p.Name, len(p.Apps)) }
