// Package store persists domain.State as a JSON file, atomically.
package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/carlos-frey/blockd/internal/domain"
)

// File stores the state at Path. A missing file loads as an empty state.
type File struct {
	Path string
}

func NewFile(path string) *File { return &File{Path: path} }

func (f *File) Load() (domain.State, error) {
	var st domain.State
	b, err := os.ReadFile(f.Path)
	if errors.Is(err, fs.ErrNotExist) {
		st.Normalize()
		return st, nil
	}
	if err != nil {
		return st, err
	}
	if err := json.Unmarshal(b, &st); err != nil {
		return st, fmt.Errorf("estado corrompido em %s: %w", f.Path, err)
	}
	st.Normalize()
	return st, nil
}

func (f *File) Save(st domain.State) error {
	st.Normalize()
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(f.Path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(f.Path), ".state-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(append(b, '\n')); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), f.Path)
}

// Memory is an in-memory store for tests.
type Memory struct{ State domain.State }

func (m *Memory) Load() (domain.State, error) { st := m.State; st.Normalize(); return st, nil }
func (m *Memory) Save(st domain.State) error  { st.Normalize(); m.State = st; return nil }
