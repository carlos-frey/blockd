package gui

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"
	"time"

	"github.com/carlos-frey/blockd/internal/api"
	"github.com/carlos-frey/blockd/internal/domain"
)

// TestCLIControllerAgainstDaemon exercises every Controller method through the
// real blockctl binary. It needs a running daemon (a dry run is enough):
//
//	blockd --dry-run --state-dir /tmp/x --socket /tmp/x/s.sock &
//	go build -o bin/ ./cmd/blockctl
//	BLOCKD_IT_SOCKET=/tmp/x/s.sock go test ./internal/gui -run CLIController
func TestCLIControllerAgainstDaemon(t *testing.T) {
	sock := os.Getenv("BLOCKD_IT_SOCKET")
	if sock == "" {
		t.Skip("BLOCKD_IT_SOCKET não definido")
	}
	_, file, _, _ := runtime.Caller(0)
	c := &CLIController{Bin: filepath.Join(filepath.Dir(file), "../../bin/blockctl"), Socket: sock}
	ctx := context.Background()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	blocked := func() []string {
		st, err := c.Status(ctx)
		must(err)
		var ids []string
		for _, a := range st.Blocked {
			ids = append(ids, a.ID)
		}
		return ids
	}

	must(c.ProfileOff(ctx, "redes-sociais", "games", "video", "trabalho", "foco-total"))
	must(c.ProfileOn(ctx, "video"))
	if !slices.Contains(blocked(), "youtube") {
		t.Fatal("youtube should be blocked by video")
	}
	err := c.Unblock(ctx, "youtube")
	if domain.AsError(err).Code != domain.CodeBlockedByProfile {
		t.Fatalf("unblock held app: %v", err)
	}
	must(c.Block(ctx, "uol"))
	must(c.Unblock(ctx, "uol"))

	must(c.AppAdd(ctx, "IT Test App", "Testes", []string{"it-test.example"}, []string{"ittest"}))
	must(c.ProfileCreate(ctx, "IT perfil", "criado no teste", []string{"it-test-app", "uol"}, true))
	if b := blocked(); !slices.Contains(b, "it-test-app") || !slices.Contains(b, "uol") {
		t.Fatalf("blocked = %v", b)
	}
	must(c.ProfileUpdate(ctx, "it-perfil", "IT perfil 2", "", []string{"uol"}))
	ps, err := c.Profiles(ctx)
	must(err)
	i := slices.IndexFunc(ps, func(p api.ProfileView) bool { return p.ID == "it-perfil" })
	if i < 0 || ps[i].Name != "IT perfil 2" || !slices.Equal(ps[i].Apps, []string{"uol"}) {
		t.Fatalf("profile after update = %+v", ps)
	}
	must(c.ProfileClone(ctx, "it-perfil"))
	must(c.ProfileRemove(ctx, "it-perfil-2-copia"))
	must(c.ProfileRemove(ctx, "it-perfil"))
	must(c.AppRemove(ctx, "it-test-app"))
	cat, err := c.Catalog(ctx)
	must(err)
	if slices.ContainsFunc(cat, func(a api.AppView) bool { return a.ID == "it-test-app" }) {
		t.Fatal("custom app still in catalog")
	}
	must(c.Sync(ctx))
	must(c.ProfileOff(ctx, "video"))

	// Schedules: a free 24/7 one is active right away and can be undone.
	allWeek := []domain.Window{{Days: []int{0, 1, 2, 3, 4, 5, 6}, Start: "00:00", End: "00:00"}}
	must(c.ScheduleSave(ctx, "", ScheduleSpec{Name: "IT agenda", Profiles: []string{"games"}, Windows: allWeek}))
	sched := func() api.ScheduleView {
		vs, err := c.Schedules(ctx)
		must(err)
		i := slices.IndexFunc(vs, func(v api.ScheduleView) bool { return v.ID == "it-agenda" })
		if i < 0 {
			t.Fatalf("schedule missing: %+v", vs)
		}
		return vs[i]
	}
	if !sched().Active || !slices.Contains(blocked(), "steam") {
		t.Fatal("24/7 schedule should block games now")
	}
	must(c.SchedulePause(ctx, "it-agenda", ""))
	if sched().Active || slices.Contains(blocked(), "steam") {
		t.Fatal("free pause should release now")
	}
	must(c.ScheduleResume(ctx, "it-agenda"))
	tomorrow := time.Now().AddDate(0, 0, 1).Format(domain.DateLayout)
	must(c.ScheduleSkip(ctx, "it-agenda", tomorrow, false))
	if v := sched(); !v.Active || !slices.Equal(v.Skip, []string{tomorrow}) {
		t.Fatalf("after skip = %+v", v)
	}
	must(c.ScheduleSkip(ctx, "it-agenda", tomorrow, true))
	// The guard and windows go through the editor's path. A window that is
	// not running now keeps the schedule removable (locks are covered by the
	// engine tests).
	later := []domain.Window{{Days: []int{int(time.Now().Add(-48 * time.Hour).Weekday())}, Start: "03:00", End: "03:01"}}
	friction := domain.Guard{Mode: domain.GuardFriction, DelayMinutes: 1, Phrase: "it frase"}
	must(c.ScheduleSave(ctx, "it-agenda", ScheduleSpec{Name: "IT agenda 2", Profiles: []string{"games"}, Windows: later, Guard: friction}))
	if v := sched(); v.Name != "IT agenda 2" || v.Guard != friction || v.Active || len(v.Windows) != 1 || v.Windows[0].Start != "03:00" {
		t.Fatalf("after edit = %+v", v)
	}
	must(c.ScheduleEnable(ctx, "it-agenda", false))
	if sched().Enabled {
		t.Fatal("schedule should be off")
	}
	must(c.ScheduleRemove(ctx, "it-agenda"))

	// Focus sessions (the daemon may already be running others).
	focusIDs := func() []string {
		st, err := c.Status(ctx)
		must(err)
		var ids []string
		for _, f := range st.Focus {
			ids = append(ids, f.ID)
		}
		return ids
	}
	before := focusIDs()
	must(c.FocusStart(ctx, []string{"games"}, 30, domain.Guard{}))
	after := focusIDs()
	if len(after) != len(before)+1 {
		t.Fatalf("focus before %v, after %v", before, after)
	}
	id := slices.DeleteFunc(after, func(x string) bool { return slices.Contains(before, x) })[0]
	must(c.FocusExtend(ctx, id, 15))
	must(c.FocusStop(ctx, id, ""))
	if slices.Contains(focusIDs(), id) {
		t.Fatal("free focus stop should end it")
	}
}
