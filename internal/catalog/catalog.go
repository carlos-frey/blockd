// Package catalog embeds the builtin apps and ready-made profiles.
package catalog

import (
	_ "embed"
	"fmt"
	"sync"

	"go.yaml.in/yaml/v3"

	"github.com/carlos-frey/blockd/internal/domain"
)

//go:embed catalog.yaml
var raw []byte

type file struct {
	Apps     []domain.App     `yaml:"apps"`
	Profiles []domain.Profile `yaml:"profiles"`
}

var (
	once     sync.Once
	apps     []domain.App
	profiles []domain.Profile
	loadErr  error
)

// Builtin returns the embedded apps and profiles, validated.
func Builtin() ([]domain.App, []domain.Profile, error) {
	once.Do(func() { apps, profiles, loadErr = parse(raw) })
	return apps, profiles, loadErr
}

func parse(b []byte) ([]domain.App, []domain.Profile, error) {
	var f file
	if err := yaml.Unmarshal(b, &f); err != nil {
		return nil, nil, fmt.Errorf("catálogo embutido: %w", err)
	}
	ids := map[string]bool{}
	for i := range f.Apps {
		f.Apps[i].Builtin = true
		if err := f.Apps[i].Validate(); err != nil {
			return nil, nil, err
		}
		if ids[f.Apps[i].ID] {
			return nil, nil, fmt.Errorf("catálogo embutido: app duplicado %q", f.Apps[i].ID)
		}
		ids[f.Apps[i].ID] = true
	}
	pids := map[string]bool{}
	for i, p := range f.Profiles {
		f.Profiles[i].Builtin = true
		if !domain.ValidID(p.ID) || pids[p.ID] {
			return nil, nil, fmt.Errorf("catálogo embutido: perfil inválido ou duplicado %q", p.ID)
		}
		pids[p.ID] = true
		for _, a := range p.Apps {
			if !ids[a] {
				return nil, nil, fmt.Errorf("catálogo embutido: perfil %q referencia app inexistente %q", p.ID, a)
			}
		}
	}
	return f.Apps, f.Profiles, nil
}
