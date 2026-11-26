// Package nrpt is the Windows DNS sinkhole, the counterpart of the dnsmasq
// backend:
//
//	app → DNS Client (Dnscache) ─ NRPT: blocked name? ─▶ 127.0.0.153 (blockd) → NXDOMAIN
//	                            └─ anything else ──────▶ the adapter's DNS servers
//
// The Name Resolution Policy Table routes queries by namespace: each blocked
// domain gets "x.com" (the name itself) and ".x.com" (every subdomain). Only
// blocked names reach the sinkhole, so every other lookup keeps its normal
// path, and nothing is left behind when the rules are removed.
//
// The rules live in the registry key Windows itself uses for local NRPT
// rules (Add-DnsClientNrptRule writes there too). Chromium-based browsers see
// NRPT rules and switch to the system resolver, so they obey them as well.
//
// The sinkhole runs inside the daemon. If the service stops, the rules stay
// and blocked names fail with a timeout instead of NXDOMAIN: still blocked.
//
// This file and sinkhole.go are pure; the registry side is in
// nrpt_windows.go.
package nrpt

import (
	"fmt"
	"regexp"
	"strconv"
)

// MaxNamesPerRule keeps each rule small; Windows handles long lists poorly
// (Tailscale splits at the same size).
const MaxNamesPerRule = 50

// Namespaces turns the blocked domains into NRPT name lists, one per rule.
func Namespaces(domains []string) [][]string {
	var out [][]string
	var cur []string
	for _, d := range domains {
		if d == "" {
			continue
		}
		if len(cur)+2 > MaxNamesPerRule {
			out = append(out, cur)
			cur = nil
		}
		cur = append(cur, d, "."+d)
	}
	if len(cur) > 0 {
		out = append(out, cur)
	}
	return out
}

// Comment is stored in every rule: the plan fingerprint and how many rules
// it was split into, so a deleted rule shows up as drift.
func Comment(fp string, rules int) string {
	return fmt.Sprintf("blockd fp=%s rules=%d", fp, rules)
}

var commentRe = regexp.MustCompile(`^blockd fp=([0-9a-f]+) rules=(\d+)$`)

// ParseComment is the inverse of Comment.
func ParseComment(c string) (fp string, rules int, ok bool) {
	m := commentRe.FindStringSubmatch(c)
	if m == nil {
		return "", 0, false
	}
	n, err := strconv.Atoi(m[2])
	if err != nil {
		return "", 0, false
	}
	return m[1], n, true
}

// FingerprintOf checks the comments read back from the installed rules: all
// must agree on the fingerprint and on how many rules there are.
func FingerprintOf(comments []string) string {
	if len(comments) == 0 {
		return ""
	}
	var fp string
	for i, c := range comments {
		f, n, ok := ParseComment(c)
		if !ok || n != len(comments) || (i > 0 && f != fp) {
			return ""
		}
		fp = f
	}
	return fp
}
