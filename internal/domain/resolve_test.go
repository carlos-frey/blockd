package domain

import (
	"slices"
	"testing"
	"time"
)

func testCatalog(st State) *Catalog {
	apps := []App{
		{ID: "insta", Name: "Instagram", Domains: []Domain{{Name: "instagram.com"}}},
		{ID: "tiktok", Name: "TikTok", Domains: []Domain{{Name: "tiktok.com", IPBlock: true}}},
		{ID: "discord", Name: "Discord", Domains: []Domain{{Name: "discord.com"}}, Processes: []string{"Discord"}},
		{ID: "doh", Name: "DoH", IPs: []string{"1.1.1.1", "2606:4700:4700::1111"}, Ports: []uint16{443}},
	}
	profiles := []Profile{
		{ID: "social", Name: "Social", Apps: []string{"insta", "tiktok", "doh"}},
		{ID: "chat", Name: "Chat", Apps: []string{"discord", "doh"}},
	}
	return NewCatalog(apps, profiles, st)
}

func TestResolveUnionAndReasons(t *testing.T) {
	st := State{ActiveProfiles: []string{"social", "chat"}, ManualApps: []string{"insta"}}
	eff := Resolve(testCatalog(st), st, time.Time{})

	if got := eff.IDs(); !slices.Equal(got, []string{"discord", "doh", "insta", "tiktok"}) {
		t.Fatalf("ids = %v", got)
	}
	if got := eff["insta"]; !slices.Equal(got, []string{"manual", "profile:social"}) {
		t.Fatalf("insta reasons = %v", got)
	}
	if got := eff["doh"]; !slices.Equal(got, []string{"profile:chat", "profile:social"}) {
		t.Fatalf("doh reasons = %v", got)
	}
	if got := eff.HoldingProfiles("doh"); !slices.Equal(got, []string{"chat", "social"}) {
		t.Fatalf("holding = %v", got)
	}
}

func TestResolveDeactivatingKeepsSharedApps(t *testing.T) {
	st := State{ActiveProfiles: []string{"chat"}}
	eff := Resolve(testCatalog(st), st, time.Time{})
	if _, ok := eff["doh"]; !ok {
		t.Fatal("doh should stay blocked through chat after social is off")
	}
	if _, ok := eff["insta"]; ok {
		t.Fatal("insta should be released")
	}
}

func TestResolveIgnoresUnknownIDs(t *testing.T) {
	st := State{ActiveProfiles: []string{"ghost"}, ManualApps: []string{"gone"}}
	if eff := Resolve(testCatalog(st), st, time.Time{}); len(eff) != 0 {
		t.Fatalf("expected nothing blocked, got %v", eff)
	}
}

func TestBuildPlan(t *testing.T) {
	st := State{ActiveProfiles: []string{"social", "chat"}}
	cat := testCatalog(st)
	p := BuildPlan(cat, Resolve(cat, st, time.Time{}))
	if !slices.Equal(p.DNSDomains, []string{"discord.com", "instagram.com", "tiktok.com"}) {
		t.Fatalf("dns = %v", p.DNSDomains)
	}
	if !slices.Equal(p.IPDomains, []string{"tiktok.com"}) {
		t.Fatalf("ip domains = %v", p.IPDomains)
	}
	if !slices.Equal(p.Processes, []string{"Discord"}) {
		t.Fatalf("procs = %v", p.Processes)
	}
	if len(p.Static) != 2 || p.Static[0].Prefix.String() != "1.1.1.1/32" || p.Static[0].Ports[0] != 443 {
		t.Fatalf("static = %+v", p.Static)
	}
}

func TestSlugify(t *testing.T) {
	cases := map[string]string{
		"Redes Sociais":       "redes-sociais",
		"  Vídeo & streaming": "video-e-streaming",
		"Foco!!! total":       "foco-total",
		"Ação":                "acao",
	}
	for in, want := range cases {
		if got := Slugify(in); got != want {
			t.Errorf("Slugify(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestValidateNormalizesDomains(t *testing.T) {
	a := App{ID: "x", Name: "X", Domains: []Domain{{Name: " *.Example.COM "}}}
	if err := a.Validate(); err != nil {
		t.Fatal(err)
	}
	if a.Domains[0].Name != "example.com" {
		t.Fatalf("got %q", a.Domains[0].Name)
	}
	bad := App{ID: "Bad ID", Name: "x", Domains: []Domain{{Name: "a.com"}}}
	if err := bad.Validate(); AsError(err).Code != CodeInvalidArgument {
		t.Fatalf("expected invalid_argument, got %v", err)
	}
}
