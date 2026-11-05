// Package cli implements blockctl. Every command maps to one api method; with
// --json the daemon's response envelope is printed as is, which is what the
// GUI consumes.
package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/carlos-frey/blockd/internal/api"
	"github.com/carlos-frey/blockd/internal/domain"
	"github.com/carlos-frey/blockd/internal/ipc"
)

// Exit codes.
const (
	ExitOK          = 0
	ExitError       = 1
	ExitUsage       = 2
	ExitUnavailable = 3
)

type app struct {
	json   bool
	socket string
	out    io.Writer
	color  bool
}

// exitError carries the exit code out of cobra.
type exitError struct{ code int }

func (e exitError) Error() string { return fmt.Sprintf("exit %d", e.code) }

// Main runs blockctl and returns the process exit code.
func Main(args []string, stdout, stderr io.Writer) int {
	a := &app{out: stdout, color: isTerminal(stdout)}
	root := a.rootCmd()
	root.SetArgs(args)
	root.SetOut(stdout)
	root.SetErr(stderr)
	err := root.Execute()
	var ee exitError
	switch {
	case err == nil:
		return ExitOK
	case errors.As(err, &ee):
		return ee.code
	default:
		// cobra usage errors (unknown flag, wrong arg count)
		if a.json {
			a.printJSON(api.Response{Error: domain.Errorf(domain.CodeInvalidArgument, "%v", err)})
		} else {
			fmt.Fprintf(stderr, "erro: %v\n", err)
		}
		return ExitUsage
	}
}

func (a *app) rootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "blockctl",
		Short: "Controla o blockd: catálogo de apps, perfis de bloqueio e status",
		Long: `blockctl conversa com o daemon blockd para bloquear apps e sites que viciam.

Bloqueie apps avulsos (blockctl app block instagram) ou ative perfis
(blockctl profile on redes-sociais). O que fica bloqueado é a união dos
perfis ativos com os apps avulsos, os perfis das agendas que estão valendo
(blockctl schedule) e os de focos temporários (blockctl focus).

Use --json em qualquer comando para saída de máquina: {"ok":true,"data":...}
ou {"ok":false,"error":{"code":"...","message":"..."}}.`,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.PersistentFlags().BoolVar(&a.json, "json", false, "saída JSON (envelope ok/data/error)")
	root.PersistentFlags().StringVar(&a.socket, "socket", ipc.SocketPath(), "socket do daemon")
	root.AddCommand(a.statusCmd(), a.syncCmd(), a.catalogCmd(), a.appCmd(), a.profileCmd(), a.scheduleCmd(), a.focusCmd())
	return root
}

// call performs one request and renders it; render gets the decoded data.
func call[T any](a *app, method string, params any, render func(T)) error {
	c := ipc.NewClient()
	c.Path = a.socket
	resp := c.Raw(context.Background(), method, params)
	if a.json {
		a.printJSON(resp)
	}
	if !resp.OK {
		if !a.json {
			fmt.Fprintf(os.Stderr, "%s %s\n", a.paint("erro:", red), resp.Error.Message)
		}
		if resp.Error != nil && resp.Error.Code == domain.CodeDaemonUnavailable {
			return exitError{ExitUnavailable}
		}
		return exitError{ExitError}
	}
	if a.json {
		return nil
	}
	var v T
	if err := json.Unmarshal(resp.Data, &v); err != nil {
		return err
	}
	render(v)
	return nil
}

func (a *app) printJSON(r api.Response) {
	enc := json.NewEncoder(a.out)
	enc.SetIndent("", "  ")
	_ = enc.Encode(r)
}

// splitList accepts repeated flags and comma separated values.
func splitList(vals []string) []string {
	var out []string
	for _, v := range vals {
		for _, p := range strings.Split(v, ",") {
			if p = strings.TrimSpace(p); p != "" {
				out = append(out, p)
			}
		}
	}
	return out
}

func isTerminal(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok || os.Getenv("NO_COLOR") != "" {
		return false
	}
	st, err := f.Stat()
	return err == nil && st.Mode()&os.ModeCharDevice != 0
}

const (
	red    = "31"
	green  = "32"
	yellow = "33"
	dim    = "2"
	bold   = "1"
	cyan   = "36"
)

func (a *app) paint(s, code string) string {
	if !a.color {
		return s
	}
	return "\x1b[" + code + "m" + s + "\x1b[0m"
}
