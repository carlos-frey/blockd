//go:build linux

package main

import (
	"errors"
	"flag"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/carlos-frey/blockd/internal/backend"
	"github.com/carlos-frey/blockd/internal/backend/dnsmasq"
	"github.com/carlos-frey/blockd/internal/backend/kick"
	"github.com/carlos-frey/blockd/internal/backend/nft"
	"github.com/carlos-frey/blockd/internal/backend/proctrack"
	"github.com/carlos-frey/blockd/internal/backend/resolver"
	"github.com/carlos-frey/blockd/internal/engine"
)

type platformOptions struct {
	jail string
}

func (p *platformOptions) register(fs *flag.FlagSet) {
	fs.StringVar(&p.jail, "jail", "blockd.jail", "cgroup (relativo à raiz cgroup2) dos apps sem rede")
}

const (
	groupHelp = "grupo com acesso ao socket (vazio = só root)"
	stopHint  = "systemctl stop blockd"
)

func defaultStateDir() string { return "/var/lib/blockd" }

func checkPrivileges() error {
	if os.Geteuid() != 0 {
		return errors.New("blockd precisa rodar como root (ou use --dry-run)")
	}
	return nil
}

// wireSystem: nftables, dnsmasq + systemd-resolved, cgroup jail.
func wireSystem(o options, d *engine.Deps, log *slog.Logger) error {
	dnsCfg := dnsmasq.DefaultConfig()
	dnsCfg.ConfPath = filepath.Join(o.stateDir, "dnsmasq.conf")
	d.Network = nft.New(o.jail)
	d.DNS = dnsmasq.New(dnsCfg)
	d.Procs = proctrack.New(o.jail, log)
	d.Kicker = kick.New()
	d.Resolver = &resolver.Upstream{ResolvFile: dnsCfg.ResolvFile, Fallback: dnsCfg.Fallback}
	return nil
}

func newKicker(*slog.Logger) backend.SessionKicker { return kick.New() }

func maybeService(options) (bool, int) { return false, 0 }
