// blockd is the privileged daemon: it owns the state, enforces it with the
// platform backends and serves blockctl on a Unix socket.
//
//	blockd              run the daemon (systemd unit, or Windows service)
//	blockd teardown     remove every rule/redirect; the saved state is kept
//	blockd kick         make open browsers drop cached DNS and sessions
//	blockd --dry-run    run without touching the system (fake backends)
//
// The concrete backends are picked in platform_<os>.go; this file only holds
// what every platform shares.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/carlos-frey/blockd/internal/backend/fake"
	"github.com/carlos-frey/blockd/internal/catalog"
	"github.com/carlos-frey/blockd/internal/engine"
	"github.com/carlos-frey/blockd/internal/ipc"
	"github.com/carlos-frey/blockd/internal/store"
)

type options struct {
	stateDir string
	socket   string
	group    string
	dryRun   bool
	verbose  bool
	platformOptions
}

func main() {
	var o options
	fs := flag.NewFlagSet("blockd", flag.ExitOnError)
	fs.StringVar(&o.stateDir, "state-dir", defaultStateDir(), "diretório do estado")
	fs.StringVar(&o.socket, "socket", ipc.SocketPath(), "socket de controle")
	fs.StringVar(&o.group, "group", "blockd", groupHelp)
	fs.BoolVar(&o.dryRun, "dry-run", false, "não mexe no sistema: backends falsos, só log")
	fs.BoolVar(&o.verbose, "v", false, "log detalhado")
	o.platformOptions.register(fs)
	fs.Usage = func() {
		fmt.Fprintf(fs.Output(), "uso: blockd [flags] [teardown|kick]\n\n")
		fs.PrintDefaults()
	}
	_ = fs.Parse(os.Args[1:])

	// Under the Windows service manager the process has no console: the
	// service wrapper runs the daemon itself and logs to a file.
	if handled, code := maybeService(o); handled {
		os.Exit(code)
	}

	log := newLogger(os.Stderr, o.verbose)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var err error
	switch fs.Arg(0) {
	case "":
		err = run(ctx, o, log)
	case "teardown":
		err = teardown(ctx, o, log)
	case "kick":
		// The daemon does this by itself when a domain gets blocked.
		err = newKicker(log).Kick(ctx)
	default:
		fs.Usage()
		os.Exit(2)
	}
	if err != nil {
		log.Error("blockd", "err", err)
		os.Exit(1)
	}
}

func newLogger(w io.Writer, verbose bool) *slog.Logger {
	level := slog.LevelInfo
	if verbose {
		level = slog.LevelDebug
	}
	return slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{Level: level}))
}

// build is the composition root: dry runs get the fakes, real runs the
// platform backends from wireSystem.
func build(o options, log *slog.Logger) (*engine.Service, error) {
	apps, profiles, err := catalog.Builtin()
	if err != nil {
		return nil, err
	}
	d := engine.Deps{
		Store:           store.NewFile(filepath.Join(o.stateDir, "state.json")),
		Log:             log,
		BuiltinApps:     apps,
		BuiltinProfiles: profiles,
		DryRun:          o.dryRun,
	}
	if o.dryRun {
		d.Network = &fake.Network{Log: log}
		d.DNS = &fake.DNS{Log: log}
		d.Procs = &fake.Procs{}
		d.Resolver = fake.Resolver{}
		d.Kicker = &fake.Kicker{Log: log}
		return engine.New(d)
	}
	if err := checkPrivileges(); err != nil {
		return nil, err
	}
	if err := wireSystem(o, &d, log); err != nil {
		return nil, err
	}
	return engine.New(d)
}

func run(ctx context.Context, o options, log *slog.Logger) error {
	svc, err := build(o, log)
	if err != nil {
		return err
	}
	group := o.group
	if o.dryRun {
		group = ""
	}
	if err := os.MkdirAll(filepath.Dir(o.socket), 0o755); err != nil {
		return err
	}
	ln, err := ipc.Listen(o.socket, group)
	if err != nil {
		return err
	}
	defer os.Remove(o.socket)
	log.Info("blockd no ar", "socket", o.socket, "dry_run", o.dryRun)

	errc := make(chan error, 2)
	go func() { errc <- svc.Run(ctx) }()
	go func() { errc <- ipc.NewServer(svc, log).Serve(ctx, ln) }()
	select {
	case <-ctx.Done():
		return nil
	case err := <-errc:
		return err
	}
}

func teardown(ctx context.Context, o options, log *slog.Logger) error {
	// With the daemon up, the next reconcile would put everything back.
	if conn, err := net.DialTimeout("unix", o.socket, time.Second); err == nil {
		conn.Close()
		return errors.New("o blockd está rodando e reaplicaria tudo; pare antes: " + stopHint)
	}
	svc, err := build(o, log)
	if err != nil {
		return err
	}
	if err := svc.Teardown(ctx); err != nil {
		return err
	}
	log.Info("bloqueios removidos do sistema; o estado salvo foi mantido e volta a valer no próximo start")
	return nil
}
