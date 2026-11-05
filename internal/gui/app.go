// Package gui is blockd-gui: a Dear ImGui (giu) front-end that controls the
// daemon exclusively through a Controller (by default, blockctl --json).
package gui

import (
	"context"
	"errors"
	"slices"
	"sync"
	"time"

	g "github.com/AllenDang/giu"

	"github.com/carlos-frey/blockd/internal/api"
	"github.com/carlos-frey/blockd/internal/domain"
)

type page int

const (
	pageOverview page = iota
	pageCatalog
	pageProfiles
	pageSchedule
)

const refreshEvery = 2 * time.Second

type toastKind int

const (
	toastOK toastKind = iota
	toastErr
)

type toast struct {
	msg   string
	kind  toastKind
	until time.Time
}

// snapshot is the data a frame renders; it is replaced wholesale by refresh.
type snapshot struct {
	loaded    bool
	connErr   string
	status    api.StatusView
	catalog   []api.AppView
	profiles  []api.ProfileView
	schedules []api.ScheduleView
	at        time.Time
}

// App holds the data model (shared with goroutines, guarded by mu) and the
// UI state (touched only from the render thread).
type App struct {
	ctl Controller

	mu      sync.Mutex
	data    snapshot
	pending map[string]bool // optimistic value for in-flight toggles, by key
	toasts  []toast
	kick    chan struct{}

	// UI state, render thread only.
	page       page
	search     string
	category   string
	editor     *profileEditor
	appForm    *appForm
	confirm    *confirmDialog
	schedEd    *scheduleEditor
	focusForm  *focusForm
	pauseDlg   *pauseDialog
	theme      *theme
	anims      map[string]float32
	openOnLoad string
}

// Options tune the initial screen.
type Options struct {
	Page string // overview | catalog | profiles | schedule
	Open string // new-profile | new-app | new-schedule | focus
}

func NewApp(ctl Controller, o Options) *App {
	a := newApp(ctl)
	switch o.Page {
	case "catalog", "catalogo":
		a.page = pageCatalog
	case "profiles", "perfis":
		a.page = pageProfiles
	case "schedule", "agenda":
		a.page = pageSchedule
	}
	a.openOnLoad = o.Open
	return a
}

func newApp(ctl Controller) *App {
	return &App{ctl: ctl, pending: map[string]bool{}, kick: make(chan struct{}, 1), theme: newTheme()}
}

// start launches the background refresher.
func (a *App) start(ctx context.Context) {
	go func() {
		t := time.NewTicker(refreshEvery)
		defer t.Stop()
		for {
			a.refresh(ctx)
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			case <-a.kick:
			}
		}
	}()
}

func (a *App) refreshSoon() {
	select {
	case a.kick <- struct{}{}:
	default:
	}
}

func (a *App) refresh(ctx context.Context) {
	var (
		s   snapshot
		wg  sync.WaitGroup
		mu  sync.Mutex
		err error
	)
	keep := func(e error) {
		mu.Lock()
		if err == nil {
			err = e
		}
		mu.Unlock()
	}
	wg.Add(4)
	go func() {
		defer wg.Done()
		v, e := a.ctl.Status(ctx)
		s.status = v
		keep(e)
	}()
	go func() {
		defer wg.Done()
		v, e := a.ctl.Catalog(ctx)
		s.catalog = v
		keep(e)
	}()
	go func() {
		defer wg.Done()
		v, e := a.ctl.Profiles(ctx)
		s.profiles = v
		keep(e)
	}()
	go func() {
		defer wg.Done()
		v, e := a.ctl.Schedules(ctx)
		s.schedules = v
		keep(e)
	}()
	wg.Wait()

	a.mu.Lock()
	if err != nil {
		a.data.connErr = domain.AsError(err).Message
		a.data.loaded = a.data.loaded && a.data.connErr == ""
	} else {
		s.loaded, s.at = true, time.Now()
		a.data = s
	}
	a.mu.Unlock()
	g.Update()
}

func (a *App) snapshot() (snapshot, map[string]bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.toasts = slices.DeleteFunc(a.toasts, func(t toast) bool { return time.Now().After(t.until) })
	return a.data, maps(a.pending)
}

func maps(m map[string]bool) map[string]bool {
	out := make(map[string]bool, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func (a *App) notify(kind toastKind, msg string) {
	a.mu.Lock()
	a.toasts = append(a.toasts, toast{msg: msg, kind: kind, until: time.Now().Add(4500 * time.Millisecond)})
	if len(a.toasts) > 4 {
		a.toasts = a.toasts[len(a.toasts)-4:]
	}
	a.mu.Unlock()
	g.Update()
}

// do runs an action off the render thread. key/optimistic make the matching
// toggle show its new value immediately; it is dropped once fresh data lands.
func (a *App) do(key string, optimistic bool, okMsg string, fn func(ctx context.Context) error) {
	if key != "" {
		a.mu.Lock()
		if _, busy := a.pending[key]; busy {
			a.mu.Unlock()
			return
		}
		a.pending[key] = optimistic
		a.mu.Unlock()
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		err := fn(ctx)
		a.refresh(ctx)
		if key != "" {
			a.mu.Lock()
			delete(a.pending, key)
			a.mu.Unlock()
		}
		switch {
		case err != nil:
			var de *domain.Error
			msg := err.Error()
			if errors.As(err, &de) {
				msg = de.Message
			}
			a.notify(toastErr, msg)
		case okMsg != "":
			a.notify(toastOK, okMsg)
		}
		g.Update()
	}()
}

func (a *App) currentToasts() []toast {
	a.mu.Lock()
	defer a.mu.Unlock()
	return slices.Clone(a.toasts)
}
