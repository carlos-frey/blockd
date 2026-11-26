//go:build windows

package wfp

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"unsafe"

	"github.com/tailscale/wf"
	"golang.org/x/sys/windows"
)

// Fixed identities of the blockd objects in WFP.
var (
	providerID = wf.ProviderID(mustGUID("{8c671caf-44a9-469b-ae0e-392b6ec22ef7}"))
	sublayerID = wf.SublayerID(mustGUID("{f3a4fd04-f250-4fc2-abda-a8071969f64f}"))
)

// WFP error codes (fwpmu.h).
const (
	fwpEFilterNotFound = 0x80320003
	fwpEAlreadyExists  = 0x80320009
)

var (
	fwpuclnt                 = windows.NewLazySystemDLL("fwpuclnt.dll")
	procFwpmEngineOpen0      = fwpuclnt.NewProc("FwpmEngineOpen0")
	procFwpmEngineClose0     = fwpuclnt.NewProc("FwpmEngineClose0")
	procFwpmFilterGetByKey0  = fwpuclnt.NewProc("FwpmFilterGetByKey0")
	procFwpmFreeMemory0      = fwpuclnt.NewProc("FwpmFreeMemory0")
	errRefuseBlockEverything = errors.New("filtro sem condição bloquearia a máquina inteira; recusado")
)

func mustGUID(s string) windows.GUID {
	g, err := windows.GUIDFromString(s)
	if err != nil {
		panic(err)
	}
	return g
}

// group is a set of filters installed together. The manifest on disk maps
// group names to the filter IDs, because WFP has no cheap way to list only
// our filters: checking each ID by key is.
type group struct {
	FP      string   `json:"fp,omitempty"`
	Path    string   `json:"path,omitempty"` // jail groups: the program
	Filters []string `json:"filters"`
}

// Session is the shared connection to WFP plus the manifest. The network
// blocker and the jail both go through it.
type Session struct {
	ManifestPath string
	Log          *slog.Logger

	mu     sync.Mutex
	wf     *wf.Session
	engine windows.Handle // raw handle for FwpmFilterGetByKey0, which wf lacks
	groups map[string]group
}

func NewSession(manifestPath string, log *slog.Logger) *Session {
	return &Session{ManifestPath: manifestPath, Log: log}
}

// open connects on first use, so building the daemon does not need WFP.
// Must hold mu.
func (s *Session) open() error {
	if s.wf != nil {
		return nil
	}
	sess, err := wf.New(&wf.Options{
		Name:        "blockd",
		Description: "blockd: bloqueio de apps e sites",
		Dynamic:     false, // our objects must outlive this process
	})
	if err != nil {
		return fmt.Errorf("abrir WFP: %w", err)
	}
	var h windows.Handle
	const rpcCAuthnWinNT = 10
	if r, _, _ := procFwpmEngineOpen0.Call(0, rpcCAuthnWinNT, 0, 0, uintptr(unsafe.Pointer(&h))); r != 0 {
		sess.Close()
		return fmt.Errorf("FwpmEngineOpen0: %w", syscall.Errno(r))
	}
	if err := s.loadManifest(); err != nil {
		procFwpmEngineClose0.Call(uintptr(h))
		sess.Close()
		return err
	}
	s.wf, s.engine = sess, h
	if err := s.ensureObjects(); err != nil {
		return err
	}
	s.sweep(false)
	return nil
}

// ensureObjects (re)creates the provider and the sublayer; they may have
// been deleted by hand. Must hold mu.
func (s *Session) ensureObjects() error {
	err := s.wf.AddProvider(&wf.Provider{
		ID:          providerID,
		Name:        "blockd",
		Description: "Filtros do blockd (bloqueio de apps e sites)",
		Persistent:  true,
	})
	if err != nil && !isCode(err, fwpEAlreadyExists) {
		return fmt.Errorf("provider WFP: %w", err)
	}
	err = s.wf.AddSublayer(&wf.Sublayer{
		ID:          sublayerID,
		Name:        "blockd",
		Description: "Filtros do blockd (bloqueio de apps e sites)",
		Provider:    providerID,
		Persistent:  true,
		Weight:      0xffff, // evaluated first; a block wins arbitration anyway
	})
	if err != nil && !isCode(err, fwpEAlreadyExists) {
		return fmt.Errorf("sublayer WFP: %w", err)
	}
	return nil
}

// sweep deletes filters in our sublayer that the manifest does not know
// (left by a crash between adding and recording them), or all of them.
// It enumerates every filter on the system, which may fail on filters wf
// cannot parse, so it is only best effort. Must hold mu.
func (s *Session) sweep(all bool) {
	rules, err := s.wf.Rules()
	if err != nil {
		s.Log.Debug("wfp: não consegui listar os filtros para limpeza", "err", err)
		return
	}
	known := map[string]bool{}
	if !all {
		for _, g := range s.groups {
			for _, id := range g.Filters {
				known[strings.ToLower(id)] = true
			}
		}
	}
	for _, r := range rules {
		if r.Sublayer != sublayerID || known[strings.ToLower(windows.GUID(r.ID).String())] {
			continue
		}
		if err := s.deleteFilter(windows.GUID(r.ID)); err != nil {
			s.Log.Warn("wfp: filtro órfão não removido", "id", windows.GUID(r.ID).String(), "err", err)
		} else {
			s.Log.Info("wfp: filtro órfão removido", "name", r.Name)
		}
	}
}

func (s *Session) loadManifest() error {
	s.groups = map[string]group{}
	b, err := os.ReadFile(s.ManifestPath)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := json.Unmarshal(b, &s.groups); err != nil {
		// Unreadable: forget it; sweep reclaims the filters it listed.
		s.Log.Warn("wfp: manifesto corrompido, ignorando", "path", s.ManifestPath, "err", err)
		s.groups = map[string]group{}
	}
	return nil
}

func (s *Session) saveManifest() error {
	b, err := json.MarshalIndent(s.groups, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.ManifestPath), 0o755); err != nil {
		return err
	}
	tmp := s.ManifestPath + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.ManifestPath)
}

func (s *Session) filterExists(id windows.GUID) (bool, error) {
	var p uintptr
	r, _, _ := procFwpmFilterGetByKey0.Call(uintptr(s.engine), uintptr(unsafe.Pointer(&id)), uintptr(unsafe.Pointer(&p)))
	switch uint32(r) {
	case 0:
		procFwpmFreeMemory0.Call(uintptr(unsafe.Pointer(&p)))
		return true, nil
	case fwpEFilterNotFound:
		return false, nil
	default:
		return false, fmt.Errorf("FwpmFilterGetByKey0: %w", syscall.Errno(r))
	}
}

func (s *Session) deleteFilter(id windows.GUID) error {
	err := s.wf.DeleteRule(wf.RuleID(id))
	if err != nil && !isCode(err, fwpEFilterNotFound) {
		return err
	}
	return nil
}

// intact reports whether every filter of g is installed. Must hold mu.
func (s *Session) intact(g group) (bool, error) {
	for _, id := range g.Filters {
		guid, err := windows.GUIDFromString(id)
		if err != nil {
			return false, nil
		}
		ok, err := s.filterExists(guid)
		if err != nil || !ok {
			return false, err
		}
	}
	return true, nil
}

// pristine reports that blockd never installed anything (no session yet and
// no manifest), so reads can answer "nothing" without opening WFP and
// creating our provider on a machine that blocks nothing. Must hold mu.
func (s *Session) pristine() bool {
	if s.wf != nil {
		return false
	}
	_, err := os.Stat(s.ManifestPath)
	return errors.Is(err, fs.ErrNotExist)
}

// check returns the fingerprint of group name, or "" when it is missing or
// some of its filters are gone.
func (s *Session) check(name string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pristine() {
		return "", nil
	}
	if err := s.open(); err != nil {
		return "", err
	}
	g, found := s.groups[name]
	if !found {
		return "", nil
	}
	ok, err := s.intact(g)
	if err != nil || !ok {
		return "", err
	}
	return g.FP, nil
}

// replace installs specs as group name, then removes what the group had.
// The new filters go in before the old ones leave, so there is no window
// without a block. The manifest lists both while the swap is in progress:
// a crash in between leaves extra filters that the next replace or remove
// cleans up, never unknown ones.
func (s *Session) replace(name, fp, path string, specs []Spec) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.open(); err != nil {
		return err
	}
	if err := s.ensureObjects(); err != nil {
		return err
	}
	var rules []*wf.Rule
	for i, sp := range specs {
		r, err := toRule(sp, fmt.Sprintf("blockd %s #%d", name, i+1))
		if err != nil {
			return err
		}
		rules = append(rules, r)
	}
	old := s.groups[name]
	newIDs := make([]string, len(rules))
	for i, r := range rules {
		newIDs[i] = windows.GUID(r.ID).String()
	}
	s.groups[name] = group{Path: path, Filters: append(slices.Clone(old.Filters), newIDs...)}
	if err := s.saveManifest(); err != nil {
		s.groups[name] = old
		return err
	}
	for i, r := range rules {
		if err := s.wf.AddRule(r); err != nil {
			for _, added := range rules[:i] {
				_ = s.deleteFilter(windows.GUID(added.ID))
			}
			s.groups[name] = old
			_ = s.saveManifest()
			return fmt.Errorf("adicionar filtro WFP %q: %w", r.Name, err)
		}
	}
	var stale []string
	var errs []error
	for _, id := range old.Filters {
		guid, err := windows.GUIDFromString(id)
		if err != nil {
			continue
		}
		if err := s.deleteFilter(guid); err != nil {
			stale = append(stale, id)
			errs = append(errs, err)
		}
	}
	g := group{FP: fp, Path: path, Filters: append(newIDs, stale...)}
	if len(stale) > 0 {
		g.FP = "" // old filters still enforce the previous plan: report drift
	}
	s.groups[name] = g
	errs = append(errs, s.saveManifest())
	return errors.Join(errs...)
}

// remove deletes every filter of group name.
func (s *Session) remove(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pristine() {
		return nil
	}
	if err := s.open(); err != nil {
		return err
	}
	return s.removeLocked(name)
}

func (s *Session) removeLocked(name string) error {
	g, found := s.groups[name]
	if !found {
		return nil
	}
	var left []string
	var errs []error
	for _, id := range g.Filters {
		guid, err := windows.GUIDFromString(id)
		if err != nil {
			continue
		}
		if err := s.deleteFilter(guid); err != nil {
			left = append(left, id)
			errs = append(errs, err)
		}
	}
	if len(left) == 0 {
		delete(s.groups, name)
	} else {
		s.groups[name] = group{Path: g.Path, Filters: left}
	}
	errs = append(errs, s.saveManifest())
	return errors.Join(errs...)
}

// groupsWithPrefix returns the groups whose name starts with prefix, split
// into intact ones and broken ones (some filter missing), keyed by name.
func (s *Session) groupsWithPrefix(prefix string) (intact, broken map[string]group, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	intact, broken = map[string]group{}, map[string]group{}
	if s.pristine() {
		return intact, broken, nil
	}
	if err := s.open(); err != nil {
		return nil, nil, err
	}
	for name, g := range s.groups {
		if !strings.HasPrefix(name, prefix) {
			continue
		}
		ok, err := s.intact(g)
		if err != nil {
			return nil, nil, err
		}
		if ok {
			intact[name] = g
		} else {
			broken[name] = g
		}
	}
	return intact, broken, nil
}

// purge removes everything blockd ever put in WFP: every group, any filter
// left in the sublayer, the sublayer and the provider.
func (s *Session) purge() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.open(); err != nil {
		return err
	}
	var errs []error
	for name := range s.groups {
		errs = append(errs, s.removeLocked(name))
	}
	s.sweep(true)
	if err := s.wf.DeleteSublayer(sublayerID); err != nil && !isNotFound(err) {
		errs = append(errs, fmt.Errorf("remover sublayer WFP: %w", err))
	}
	if err := s.wf.DeleteProvider(providerID); err != nil && !isNotFound(err) {
		errs = append(errs, fmt.Errorf("remover provider WFP: %w", err))
	}
	if err := errors.Join(errs...); err != nil {
		return err
	}
	// Nothing left: the next start must not even open WFP until needed.
	if err := os.Remove(s.ManifestPath); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

func isCode(err error, code uint32) bool {
	var errno syscall.Errno
	return errors.As(err, &errno) && uint32(errno) == code
}

// isNotFound matches the FWP_E_*_NOT_FOUND family (0x80320001-0x80320008).
func isNotFound(err error) bool {
	var errno syscall.Errno
	return errors.As(err, &errno) && uint32(errno) >= 0x80320001 && uint32(errno) <= 0x80320008
}

// toRule maps a Spec to a persistent, hard block filter in our sublayer.
func toRule(sp Spec, name string) (*wf.Rule, error) {
	if sp.BlocksEverything() {
		return nil, errRefuseBlockEverything
	}
	id, err := windows.GenerateGUID()
	if err != nil {
		return nil, err
	}
	layer := wf.LayerALEAuthConnectV4
	if sp.V6 {
		layer = wf.LayerALEAuthConnectV6
	}
	var conds []*wf.Match
	for _, p := range sp.Prefixes {
		conds = append(conds, &wf.Match{Field: wf.FieldIPRemoteAddress, Op: wf.MatchTypeEqual, Value: p})
	}
	for _, proto := range sp.Protos {
		conds = append(conds, &wf.Match{Field: wf.FieldIPProtocol, Op: wf.MatchTypeEqual, Value: wf.IPProto(proto)})
	}
	for _, port := range sp.Ports {
		conds = append(conds, &wf.Match{Field: wf.FieldIPRemotePort, Op: wf.MatchTypeEqual, Value: port})
	}
	if sp.App != "" {
		appID, err := wf.AppID(sp.App)
		if err != nil {
			return nil, fmt.Errorf("AppID de %s: %w", sp.App, err)
		}
		conds = append(conds, &wf.Match{Field: wf.FieldALEAppID, Op: wf.MatchTypeEqual, Value: appID})
	}
	if sp.SkipLoopback {
		conds = append(conds, &wf.Match{Field: wf.FieldFlags, Op: wf.MatchTypeFlagsNoneSet, Value: wf.ConditionFlagIsLoopback})
	}
	return &wf.Rule{
		ID:         wf.RuleID(id),
		Name:       name,
		Layer:      layer,
		Sublayer:   sublayerID,
		Conditions: conds,
		Action:     wf.ActionBlock,
		HardAction: true,
		Persistent: true,
		Provider:   providerID,
	}, nil
}
