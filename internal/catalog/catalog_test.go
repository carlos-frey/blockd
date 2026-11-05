package catalog

import (
	"slices"
	"testing"
)

func TestBuiltinLoads(t *testing.T) {
	apps, profiles, err := Builtin()
	if err != nil {
		t.Fatal(err)
	}
	if len(apps) < 20 || len(profiles) < 4 {
		t.Fatalf("catálogo pequeno demais: %d apps, %d perfis", len(apps), len(profiles))
	}
	for _, p := range profiles {
		if !slices.Contains(p.Apps, "doh") {
			t.Errorf("perfil pronto %q deveria incluir doh", p.ID)
		}
		if slices.Contains(p.Apps, "linkedin") {
			t.Errorf("linkedin fica fora dos perfis prontos (%q)", p.ID)
		}
	}
}

func TestParseRejectsDanglingProfile(t *testing.T) {
	_, _, err := parse([]byte(`
apps:
  - {id: a, name: A, domains: [a.com]}
profiles:
  - {id: p, name: P, apps: [a, missing]}
`))
	if err == nil {
		t.Fatal("expected error for unknown app in profile")
	}
}
