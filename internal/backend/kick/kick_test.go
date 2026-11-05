package kick

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/carlos-frey/blockd/internal/backend/sysexec"
)

func calls(r *sysexec.Recorder) []string {
	var out []string
	for _, c := range r.Calls {
		out = append(out, c.String())
	}
	return out
}

func TestKickOrder(t *testing.T) {
	rec := &sysexec.Recorder{}
	k := &Kicker{Run: rec, Addr: "127.0.0.154/32"}
	if err := k.Kick(context.Background()); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"ss -K -n -u state established dport = :443",
		"ip addr add 127.0.0.154/32 dev lo",
		"ip addr del 127.0.0.154/32 dev lo",
	}
	if got := calls(rec); !slices.Equal(got, want) {
		t.Fatalf("calls =\n%q\nwant\n%q", got, want)
	}
}

func TestKickLeftoverAddress(t *testing.T) {
	rec := &sysexec.Recorder{Respond: func(c sysexec.Call) ([]byte, error) {
		if slices.Contains(c.Args, "add") {
			return nil, errors.New("RTNETLINK answers: File exists")
		}
		return nil, nil
	}}
	k := &Kicker{Run: rec, Addr: "127.0.0.154/32"}
	if err := k.Kick(context.Background()); err != nil {
		t.Fatalf("del sozinho já gera a mudança; err = %v", err)
	}
}
