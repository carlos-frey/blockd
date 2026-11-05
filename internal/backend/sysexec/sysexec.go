// Package sysexec runs system commands behind an interface, so backends can
// be tested by recording the commands instead of executing them.
package sysexec

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
)

type Runner interface {
	Run(ctx context.Context, stdin []byte, name string, args ...string) ([]byte, error)
}

// Exec runs real commands.
type Exec struct{}

func (Exec) Run(ctx context.Context, stdin []byte, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return out, fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return out, nil
}

// Call is one recorded invocation.
type Call struct {
	Name  string
	Args  []string
	Stdin string
}

func (c Call) String() string { return strings.TrimSpace(c.Name + " " + strings.Join(c.Args, " ")) }

// Recorder records calls and answers through Respond (nil output otherwise).
type Recorder struct {
	Calls   []Call
	Respond func(c Call) ([]byte, error)
}

func (r *Recorder) Run(_ context.Context, stdin []byte, name string, args ...string) ([]byte, error) {
	c := Call{Name: name, Args: args, Stdin: string(stdin)}
	r.Calls = append(r.Calls, c)
	if r.Respond != nil {
		return r.Respond(c)
	}
	return nil, nil
}
