// Package api is the wire contract shared by the daemon, blockctl and the GUI:
// method names, request params and response views. `blockctl --json` prints
// exactly these types inside a Response envelope.
package api

import (
	"encoding/json"
	"time"

	"github.com/carlos-frey/blockd/internal/backend"
	"github.com/carlos-frey/blockd/internal/domain"
)

const (
	MStatus        = "status"
	MSync          = "sync"
	MCatalogList   = "catalog.list"
	MCatalogShow   = "catalog.show"
	MCatalogAdd    = "catalog.add"
	MCatalogEdit   = "catalog.edit"
	MCatalogRemove = "catalog.rm"
	MAppBlock      = "app.block"
	MAppUnblock    = "app.unblock"
	MProfileList   = "profile.list"
	MProfileShow   = "profile.show"
	MProfileCreate = "profile.create"
	MProfileEdit   = "profile.edit"
	MProfileClone  = "profile.clone"
	MProfileRemove = "profile.rm"
	MProfileOn     = "profile.on"
	MProfileOff    = "profile.off"

	MScheduleList   = "schedule.list"
	MScheduleShow   = "schedule.show"
	MScheduleCreate = "schedule.create"
	MScheduleEdit   = "schedule.edit"
	MScheduleRemove = "schedule.rm"
	MScheduleOn     = "schedule.on"
	MScheduleOff    = "schedule.off"
	MSchedulePause  = "schedule.pause"
	MScheduleResume = "schedule.resume"
	MScheduleSkip   = "schedule.skip"

	MFocusStart  = "focus.start"
	MFocusStop   = "focus.stop"
	MFocusResume = "focus.resume"
	MFocusExtend = "focus.extend"
)

// Request is one line on the daemon socket.
type Request struct {
	Method string          `json:"method"`
	Params json.RawMessage `json:"params,omitempty"`
}

// Response is both the socket reply and the `blockctl --json` envelope.
type Response struct {
	OK    bool            `json:"ok"`
	Data  json.RawMessage `json:"data,omitempty"`
	Error *domain.Error   `json:"error,omitempty"`
}

type IDParams struct {
	ID string `json:"id"`
}

type IDsParams struct {
	IDs []string `json:"ids"`
}

type CatalogListParams struct {
	Category string `json:"category,omitempty"`
	Search   string `json:"search,omitempty"`
}

type CatalogAddParams struct {
	App domain.App `json:"app"`
}

// CatalogEditParams replaces only the fields that are set.
type CatalogEditParams struct {
	ID          string           `json:"id"`
	Name        *string          `json:"name,omitempty"`
	Category    *string          `json:"category,omitempty"`
	Description *string          `json:"description,omitempty"`
	Domains     *[]domain.Domain `json:"domains,omitempty"`
	Processes   *[]string        `json:"processes,omitempty"`
	IPs         *[]string        `json:"ips,omitempty"`
	Ports       *[]uint16        `json:"ports,omitempty"`
}

type ProfileCreateParams struct {
	Name        string   `json:"name"`
	ID          string   `json:"id,omitempty"` // derived from Name when empty
	Description string   `json:"description,omitempty"`
	Apps        []string `json:"apps,omitempty"`
	// FromBlocked adds every app that is blocked right now.
	FromBlocked bool `json:"from_blocked,omitempty"`
	// Activate turns the new profile on right away.
	Activate bool `json:"activate,omitempty"`
}

type ProfileEditParams struct {
	ID          string    `json:"id"`
	Name        *string   `json:"name,omitempty"`
	Description *string   `json:"description,omitempty"`
	SetApps     *[]string `json:"set_apps,omitempty"`
	Add         []string  `json:"add,omitempty"`
	Remove      []string  `json:"remove,omitempty"`
}

type ProfileCloneParams struct {
	ID   string `json:"id"`
	Name string `json:"name,omitempty"`
}

type ScheduleCreateParams struct {
	Name     string          `json:"name"`
	ID       string          `json:"id,omitempty"` // derived from Name when empty
	Profiles []string        `json:"profiles"`
	Windows  []domain.Window `json:"windows"`
	Guard    domain.Guard    `json:"guard"`
	// Disabled creates the schedule switched off.
	Disabled bool `json:"disabled,omitempty"`
}

// ScheduleEditParams replaces only the fields that are set.
type ScheduleEditParams struct {
	ID       string           `json:"id"`
	Name     *string          `json:"name,omitempty"`
	Profiles *[]string        `json:"profiles,omitempty"`
	Windows  *[]domain.Window `json:"windows,omitempty"`
	Guard    *domain.Guard    `json:"guard,omitempty"`
}

// PauseParams asks to switch off a schedule (until the end of the current
// occurrence) or a focus session. Phrase is required in friction mode.
type PauseParams struct {
	ID     string `json:"id"`
	Phrase string `json:"phrase,omitempty"`
}

type ScheduleSkipParams struct {
	ID   string `json:"id"`
	Date string `json:"date"` // AAAA-MM-DD
	Undo bool   `json:"undo,omitempty"`
}

// FocusStartParams runs profiles until Until, or for Minutes from now.
type FocusStartParams struct {
	Profiles []string     `json:"profiles"`
	Minutes  int          `json:"minutes,omitempty"`
	Until    *time.Time   `json:"until,omitempty"`
	Guard    domain.Guard `json:"guard"`
}

// FocusExtendParams pushes the end of a session; it can only grow.
type FocusExtendParams struct {
	ID      string     `json:"id"`
	Minutes int        `json:"minutes,omitempty"`
	Until   *time.Time `json:"until,omitempty"`
}

// AppView is a catalog app plus its live blocking state.
type AppView struct {
	domain.App
	Blocked   bool     `json:"blocked"`
	BlockedBy []string `json:"blocked_by"`
	// Profiles lists every profile (active or not) that contains the app.
	Profiles []string `json:"profiles"`
}

type ProfileView struct {
	domain.Profile
	// Active means switched on by hand; ActiveBy lists the schedules and
	// focus sessions switching it on right now.
	Active   bool     `json:"active"`
	ActiveBy []string `json:"active_by"`
}

type ScheduleView struct {
	domain.Schedule
	// Active means it is blocking now: enabled, inside an occurrence, not paused.
	Active bool `json:"active"`
	// Current is the occurrence in effect, even when paused or disabled.
	Current *domain.Occurrence `json:"current,omitempty"`
	Next    *domain.Occurrence `json:"next,omitempty"`
	Pause   *domain.Pause      `json:"pause,omitempty"`
}

type FocusView struct {
	domain.FocusSession
	Active bool          `json:"active"`
	Pause  *domain.Pause `json:"pause,omitempty"`
}

type SyncView struct {
	InSync    bool       `json:"in_sync"`
	Network   string     `json:"network"` // ok | drift | error
	DNS       string     `json:"dns"`
	LastCheck time.Time  `json:"last_check"`
	LastDrift *time.Time `json:"last_drift,omitempty"`
	LastError string     `json:"last_error,omitempty"`
	// DynamicIPs is how many resolved addresses are currently dropped.
	DynamicIPs int `json:"dynamic_ips"`
}

type StatusView struct {
	DryRun         bool                 `json:"dry_run"`
	ActiveProfiles []string             `json:"active_profiles"`
	ManualApps     []string             `json:"manual_apps"`
	Blocked        []AppView            `json:"blocked"`
	Plan           domain.Plan          `json:"plan"`
	Jailed         []backend.JailedProc `json:"jailed"`
	Sync           SyncView             `json:"sync"`
	// ActiveSchedules are the ids of the schedules blocking now.
	ActiveSchedules []string    `json:"active_schedules"`
	Focus           []FocusView `json:"focus"`
	// NextChange is when the blocked set may change by itself.
	NextChange *time.Time `json:"next_change,omitempty"`
}
