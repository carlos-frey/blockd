package gui

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/carlos-frey/blockd/internal/api"
	"github.com/carlos-frey/blockd/internal/domain"
)

// Controller is everything the GUI can ask for. The GUI only depends on this
// interface; CLIController implements it by running `blockctl --json`.
type Controller interface {
	Status(ctx context.Context) (api.StatusView, error)
	Catalog(ctx context.Context) ([]api.AppView, error)
	Profiles(ctx context.Context) ([]api.ProfileView, error)
	Sync(ctx context.Context) error

	Block(ctx context.Context, ids ...string) error
	Unblock(ctx context.Context, ids ...string) error
	ProfileOn(ctx context.Context, ids ...string) error
	ProfileOff(ctx context.Context, ids ...string) error
	ProfileCreate(ctx context.Context, name, desc string, apps []string, activate bool) error
	ProfileUpdate(ctx context.Context, id, name, desc string, apps []string) error
	ProfileClone(ctx context.Context, id string) error
	ProfileRemove(ctx context.Context, id string) error
	AppAdd(ctx context.Context, name, category string, domains, processes []string) error
	AppRemove(ctx context.Context, id string) error

	Schedules(ctx context.Context) ([]api.ScheduleView, error)
	// ScheduleSave creates the schedule when id is empty.
	ScheduleSave(ctx context.Context, id string, spec ScheduleSpec) error
	ScheduleRemove(ctx context.Context, id string) error
	ScheduleEnable(ctx context.Context, id string, on bool) error
	SchedulePause(ctx context.Context, id, phrase string) error
	ScheduleResume(ctx context.Context, id string) error
	ScheduleSkip(ctx context.Context, id, date string, undo bool) error
	FocusStart(ctx context.Context, profiles []string, minutes int, g domain.Guard) error
	FocusStop(ctx context.Context, id, phrase string) error
	FocusResume(ctx context.Context, id string) error
	FocusExtend(ctx context.Context, id string, minutes int) error
}

// ScheduleSpec is everything the schedule editor sets.
type ScheduleSpec struct {
	Name     string
	Profiles []string
	Windows  []domain.Window
	Guard    domain.Guard
}

// CLIController drives blockctl as a subprocess, so the GUI exercises exactly
// the same API as the terminal.
type CLIController struct {
	Bin    string // blockctl path
	Socket string // optional --socket
}

// FindBlockctl prefers a blockctl next to the GUI binary, then $PATH.
func FindBlockctl() string {
	if exe, err := os.Executable(); err == nil {
		p := filepath.Join(filepath.Dir(exe), "blockctl")
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	if p, err := exec.LookPath("blockctl"); err == nil {
		return p
	}
	return "blockctl"
}

func (c *CLIController) run(ctx context.Context, out any, args ...string) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	full := []string{"--json"}
	if c.Socket != "" {
		full = append(full, "--socket", c.Socket)
	}
	full = append(full, args...)
	cmd := exec.CommandContext(ctx, c.Bin, full...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	runErr := cmd.Run()

	var resp api.Response
	if err := json.Unmarshal(stdout.Bytes(), &resp); err != nil {
		if runErr != nil {
			return &domain.Error{Code: domain.CodeDaemonUnavailable,
				Message: fmt.Sprintf("falha ao executar %s: %v %s", c.Bin, runErr, strings.TrimSpace(stderr.String()))}
		}
		return fmt.Errorf("resposta inválida do blockctl: %w", err)
	}
	if !resp.OK {
		if resp.Error == nil {
			return &domain.Error{Code: domain.CodeInternal, Message: "erro desconhecido"}
		}
		return resp.Error
	}
	if out != nil {
		return json.Unmarshal(resp.Data, out)
	}
	return nil
}

func (c *CLIController) Status(ctx context.Context) (v api.StatusView, err error) {
	return v, c.run(ctx, &v, "status")
}

func (c *CLIController) Catalog(ctx context.Context) (v []api.AppView, err error) {
	return v, c.run(ctx, &v, "catalog", "list")
}

func (c *CLIController) Profiles(ctx context.Context) (v []api.ProfileView, err error) {
	return v, c.run(ctx, &v, "profile", "list")
}

func (c *CLIController) Sync(ctx context.Context) error { return c.run(ctx, nil, "sync") }

func (c *CLIController) Block(ctx context.Context, ids ...string) error {
	return c.run(ctx, nil, append([]string{"app", "block"}, ids...)...)
}

func (c *CLIController) Unblock(ctx context.Context, ids ...string) error {
	return c.run(ctx, nil, append([]string{"app", "unblock"}, ids...)...)
}

func (c *CLIController) ProfileOn(ctx context.Context, ids ...string) error {
	return c.run(ctx, nil, append([]string{"profile", "on"}, ids...)...)
}

func (c *CLIController) ProfileOff(ctx context.Context, ids ...string) error {
	return c.run(ctx, nil, append([]string{"profile", "off"}, ids...)...)
}

func (c *CLIController) ProfileCreate(ctx context.Context, name, desc string, apps []string, activate bool) error {
	args := []string{"profile", "create", name, "--desc", desc, "--apps", strings.Join(apps, ",")}
	if activate {
		args = append(args, "--on")
	}
	return c.run(ctx, nil, args...)
}

func (c *CLIController) ProfileUpdate(ctx context.Context, id, name, desc string, apps []string) error {
	return c.run(ctx, nil, "profile", "edit", id, "--name", name, "--desc", desc, "--set", strings.Join(apps, ","))
}

func (c *CLIController) ProfileClone(ctx context.Context, id string) error {
	return c.run(ctx, nil, "profile", "clone", id)
}

func (c *CLIController) ProfileRemove(ctx context.Context, id string) error {
	return c.run(ctx, nil, "profile", "rm", id)
}

func (c *CLIController) AppAdd(ctx context.Context, name, category string, domains, processes []string) error {
	args := []string{"catalog", "add", name, "--category", category}
	if len(domains) > 0 {
		args = append(args, "--domain", strings.Join(domains, ","))
	}
	if len(processes) > 0 {
		args = append(args, "--process", strings.Join(processes, ","))
	}
	return c.run(ctx, nil, args...)
}

func (c *CLIController) AppRemove(ctx context.Context, id string) error {
	return c.run(ctx, nil, "catalog", "rm", id)
}

func (c *CLIController) Schedules(ctx context.Context) (v []api.ScheduleView, err error) {
	return v, c.run(ctx, &v, "schedule", "list")
}

func (c *CLIController) ScheduleSave(ctx context.Context, id string, spec ScheduleSpec) error {
	args := []string{"schedule", "create", spec.Name}
	if id != "" {
		args = []string{"schedule", "edit", id, "--name", spec.Name}
	}
	args = append(args, "--profiles", strings.Join(spec.Profiles, ","), "--mode", string(spec.Guard.Mode))
	if spec.Guard.Mode == domain.GuardFriction {
		args = append(args, "--delay", fmt.Sprint(spec.Guard.DelayMinutes), "--phrase", spec.Guard.Phrase)
	}
	for _, w := range spec.Windows {
		args = append(args, "--window", w.String())
	}
	return c.run(ctx, nil, args...)
}

func (c *CLIController) ScheduleRemove(ctx context.Context, id string) error {
	return c.run(ctx, nil, "schedule", "rm", id)
}

func (c *CLIController) ScheduleEnable(ctx context.Context, id string, on bool) error {
	verb := "off"
	if on {
		verb = "on"
	}
	return c.run(ctx, nil, "schedule", verb, id)
}

func (c *CLIController) SchedulePause(ctx context.Context, id, phrase string) error {
	return c.run(ctx, nil, "schedule", "pause", id, "--phrase", phrase)
}

func (c *CLIController) ScheduleResume(ctx context.Context, id string) error {
	return c.run(ctx, nil, "schedule", "resume", id)
}

func (c *CLIController) ScheduleSkip(ctx context.Context, id, date string, undo bool) error {
	args := []string{"schedule", "skip", id, date}
	if undo {
		args = append(args, "--undo")
	}
	return c.run(ctx, nil, args...)
}

func (c *CLIController) FocusStart(ctx context.Context, profiles []string, minutes int, g domain.Guard) error {
	args := append([]string{"focus", "start"}, profiles...)
	args = append(args, "--for", fmt.Sprint(minutes), "--mode", string(g.Mode))
	if g.Mode == domain.GuardFriction {
		args = append(args, "--delay", fmt.Sprint(g.DelayMinutes), "--phrase", g.Phrase)
	}
	return c.run(ctx, nil, args...)
}

func (c *CLIController) FocusStop(ctx context.Context, id, phrase string) error {
	return c.run(ctx, nil, "focus", "stop", id, "--phrase", phrase)
}

func (c *CLIController) FocusResume(ctx context.Context, id string) error {
	return c.run(ctx, nil, "focus", "resume", id)
}

func (c *CLIController) FocusExtend(ctx context.Context, id string, minutes int) error {
	return c.run(ctx, nil, "focus", "extend", id, "--for", fmt.Sprint(minutes))
}
