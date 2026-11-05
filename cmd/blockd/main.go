// blockd is the root daemon: it owns the state, enforces it with nftables,
// dnsmasq and the process jail, and serves blockctl on a Unix socket.
//
//	blockd              run the daemon (systemd: blockd.service)
//	blockd teardown     remove every rule/redirect; the saved state is kept
//	blockd kick         make open browsers drop cached DNS and sessions
//	blockd --dry-run    run without touching the system (fake backends)
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/carlos-frey/blockd/internal/backend/dnsmasq"
	"github.com/carlos-frey/blockd/internal/backend/fake"
	"github.com/carlos-frey/blockd/internal/backend/kick"
	"github.com/carlos-frey/blockd/internal/backend/nft"
	"github.com/carlos-frey/blockd/internal/backend/proctrack"
	"github.com/carlos-frey/blockd/internal/backend/resolver"
	"github.com/carlos-frey/blockd/internal/catalog"
	"github.com/carlos-frey/blockd/internal/engine"
	"github.com/carlos-frey/blockd/internal/ipc"
	"github.com/carlos-frey/blockd/internal/store"
)

type options struct {
	stateDir string
	socket   string
	group    string
	jail     string
	dryRun   bool
	verbose  bool
}

func main() {
	var o options
	fs := flag.NewFlagSet("blockd", flag.ExitOnError)
	fs.StringVar(&o.stateDir, "state-dir", "/var/lib/blockd", "diretório do estado")
	fs.StringVar(&o.socket, "socket", ipc.SocketPath(), "socket de controle")
	fs.StringVar(&o.group, "group", "blockd", "grupo com acesso ao socket (vazio = só root)")
	fs.StringVar(&o.jail, "jail", "blockd.jail", "cgroup (relativo à raiz cgroup2) dos apps sem rede")
	fs.BoolVar(&o.dryRun, "dry-run", false, "não mexe no sistema: backends falsos, só log")
	fs.BoolVar(&o.verbose, "v", false, "log detalhado")
	fs.Usage = func() {
		fmt.Fprintf(fs.Output(), "uso: blockd [flags] [teardown|kick]\n\n")
		fs.PrintDefaults()
	}
	_ = fs.Parse(os.Args[1:])

	level := slog.LevelInfo
	if o.verbose {
		level = slog.LevelDebug
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))

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
		err = kick.New().Kick(ctx)
	default:
		fs.Usage()
		os.Exit(2)
	}
	if err != nil {
		log.Error("blockd", "err", err)
		os.Exit(1)
	}
}

// build is the composition root: the only place that picks implementations.
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
	if os.Geteuid() != 0 {
		return nil, errors.New("blockd precisa rodar como root (ou use --dry-run)")
	}
	dnsCfg := dnsmasq.DefaultConfig()
	dnsCfg.ConfPath = filepath.Join(o.stateDir, "dnsmasq.conf")
	d.Network = nft.New(o.jail)
	d.DNS = dnsmasq.New(dnsCfg)
	d.Procs = proctrack.New(o.jail, log)
	d.Kicker = kick.New()
	d.Resolver = &resolver.Upstream{ResolvFile: dnsCfg.ResolvFile, Fallback: dnsCfg.Fallback}
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
		return errors.New("o blockd está rodando e reaplicaria tudo; pare antes: systemctl stop blockd")
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
