package domain

import (
	"net/netip"
	"slices"
	"strings"
	"time"
)

// Catalog is the merged, read-only view of builtin + custom apps and profiles.
type Catalog struct {
	apps     map[string]App
	appOrder []string
	profs    map[string]Profile
	profOrd  []string
}

// NewCatalog merges builtin definitions with the user's custom ones.
// Builtins come first, in definition order; custom entries follow.
func NewCatalog(builtinApps []App, builtinProfiles []Profile, st State) *Catalog {
	c := &Catalog{apps: map[string]App{}, profs: map[string]Profile{}}
	for _, a := range builtinApps {
		a.Builtin = true
		c.addApp(a)
	}
	for _, a := range st.CustomApps {
		a.Builtin = false
		c.addApp(a)
	}
	for _, p := range builtinProfiles {
		p.Builtin = true
		c.addProfile(p)
	}
	for _, p := range st.UserProfiles {
		p.Builtin = false
		c.addProfile(p)
	}
	return c
}

func (c *Catalog) addApp(a App) {
	if _, ok := c.apps[a.ID]; !ok {
		c.appOrder = append(c.appOrder, a.ID)
	}
	c.apps[a.ID] = a
}

func (c *Catalog) addProfile(p Profile) {
	if _, ok := c.profs[p.ID]; !ok {
		c.profOrd = append(c.profOrd, p.ID)
	}
	c.profs[p.ID] = p
}

func (c *Catalog) App(id string) (App, bool)         { a, ok := c.apps[id]; return a, ok }
func (c *Catalog) Profile(id string) (Profile, bool) { p, ok := c.profs[id]; return p, ok }

func (c *Catalog) Apps() []App {
	out := make([]App, 0, len(c.appOrder))
	for _, id := range c.appOrder {
		out = append(out, c.apps[id])
	}
	return out
}

func (c *Catalog) Profiles() []Profile {
	out := make([]Profile, 0, len(c.profOrd))
	for _, id := range c.profOrd {
		out = append(out, c.profs[id])
	}
	return out
}

// Effective maps each blocked app id to the sorted reasons it is blocked.
type Effective map[string][]string

// Resolve computes the blocked set at t: manual apps ∪ apps of every profile
// switched on by hand ∪ apps of the profiles of every active schedule or focus
// session. Ids that no longer exist in the catalog are ignored.
func Resolve(c *Catalog, st State, t time.Time) Effective {
	eff := Effective{}
	add := func(app, reason string) {
		if _, ok := c.apps[app]; !ok {
			return
		}
		if !slices.Contains(eff[app], reason) {
			eff[app] = append(eff[app], reason)
		}
	}
	for _, id := range st.ManualApps {
		add(id, ReasonManual)
	}
	for _, pid := range st.ActiveProfiles {
		p, ok := c.profs[pid]
		if !ok {
			continue
		}
		for _, app := range p.Apps {
			add(app, ProfileReason(pid))
		}
	}
	for _, src := range st.ActiveSources(t) {
		for _, app := range c.SourceApps(src) {
			add(app, src.ID)
		}
	}
	for k := range eff {
		slices.Sort(eff[k])
	}
	return eff
}

// IDs returns the blocked app ids, sorted.
func (e Effective) IDs() []string {
	out := make([]string, 0, len(e))
	for id := range e {
		out = append(out, id)
	}
	slices.Sort(out)
	return out
}

// Holders lists every reason other than a manual block that keeps app
// blocked: profiles switched on by hand, schedules and focus sessions.
func (e Effective) Holders(app string) []string {
	var out []string
	for _, r := range e[app] {
		if r != ReasonManual {
			out = append(out, r)
		}
	}
	return out
}

// HoldingProfiles lists the profiles switched on by hand that block app.
func (e Effective) HoldingProfiles(app string) []string {
	var out []string
	for _, r := range e[app] {
		if id, ok := strings.CutPrefix(r, "profile:"); ok {
			out = append(out, id)
		}
	}
	return out
}

// SourceApps lists the apps of every profile of src, sorted.
func (c *Catalog) SourceApps(src Source) []string {
	var out []string
	for _, pid := range src.Profiles {
		if p, ok := c.profs[pid]; ok {
			out = append(out, p.Apps...)
		}
	}
	return sortedSet(out)
}

// SourcePlan is what src alone enforces.
func (c *Catalog) SourcePlan(src Source) Plan {
	eff := Effective{}
	for _, id := range c.SourceApps(src) {
		if _, ok := c.apps[id]; ok {
			eff[id] = []string{src.ID}
		}
	}
	return BuildPlan(c, eff)
}

// Covers reports whether p enforces at least everything in q.
func (p Plan) Covers(q Plan) bool {
	sub := func(small, big []string) bool {
		for _, s := range small {
			if !slices.Contains(big, s) {
				return false
			}
		}
		return true
	}
	for _, r := range q.Static {
		if !slices.ContainsFunc(p.Static, func(o IPRule) bool { return o.Prefix == r.Prefix && slices.Equal(o.Ports, r.Ports) }) {
			return false
		}
	}
	return sub(q.DNSDomains, p.DNSDomains) && sub(q.IPDomains, p.IPDomains) && sub(q.Processes, p.Processes)
}

// IPRule blocks a fixed prefix, optionally only on some ports (tcp+udp).
type IPRule struct {
	Prefix netip.Prefix `json:"prefix"`
	Ports  []uint16     `json:"ports,omitempty"`
}

// Plan is the desired enforcement derived from the effective set; it is what
// the backends receive.
type Plan struct {
	DNSDomains []string `json:"dns_domains"` // sinkholed (with subdomains)
	IPDomains  []string `json:"ip_domains"`  // also resolved and dropped by IP
	Processes  []string `json:"processes"`   // network cut via cgroup
	Static     []IPRule `json:"static"`
}

// BuildPlan flattens the effective apps into deduplicated, sorted lists.
func BuildPlan(c *Catalog, eff Effective) Plan {
	var p Plan
	for _, id := range eff.IDs() {
		a := c.apps[id]
		for _, d := range a.Domains {
			p.DNSDomains = append(p.DNSDomains, d.Name)
			if d.IPBlock {
				p.IPDomains = append(p.IPDomains, d.Name)
			}
		}
		p.Processes = append(p.Processes, a.Processes...)
		for _, s := range a.IPs {
			pre, err := ParsePrefix(s)
			if err != nil {
				continue // validated on insert; never expected here
			}
			p.Static = append(p.Static, IPRule{Prefix: pre, Ports: slices.Clone(a.Ports)})
		}
	}
	p.DNSDomains = sortedSet(p.DNSDomains)
	p.IPDomains = sortedSet(p.IPDomains)
	p.Processes = sortedSet(p.Processes)
	slices.SortFunc(p.Static, func(a, b IPRule) int {
		if c := a.Prefix.Addr().Compare(b.Prefix.Addr()); c != 0 {
			return c
		}
		return a.Prefix.Bits() - b.Prefix.Bits()
	})
	p.Static = slices.CompactFunc(p.Static, func(a, b IPRule) bool {
		return a.Prefix == b.Prefix && slices.Equal(a.Ports, b.Ports)
	})
	if p.Static == nil {
		p.Static = []IPRule{}
	}
	return p
}

// Empty reports whether nothing at all is to be enforced.
func (p Plan) Empty() bool {
	return len(p.DNSDomains) == 0 && len(p.Processes) == 0 && len(p.Static) == 0
}
