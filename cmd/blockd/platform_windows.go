//go:build windows

package main

import (
	"context"
	"errors"
	"flag"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sys/windows/svc"

	"github.com/carlos-frey/blockd/internal/backend"
	"github.com/carlos-frey/blockd/internal/backend/nrpt"
	"github.com/carlos-frey/blockd/internal/backend/resolver"
	"github.com/carlos-frey/blockd/internal/backend/wfp"
	"github.com/carlos-frey/blockd/internal/backend/winkick"
	"github.com/carlos-frey/blockd/internal/backend/winproc"
	"github.com/carlos-frey/blockd/internal/backend/winsys"
	"github.com/carlos-frey/blockd/internal/engine"
)

const serviceName = "blockd"

type platformOptions struct {
	sinkhole string
}

func (p *platformOptions) register(fs *flag.FlagSet) {
	fs.StringVar(&p.sinkhole, "sinkhole", nrpt.DefaultConfig().SinkholeAddr,
		"endereço de loopback do DNS que responde NXDOMAIN (porta 53)")
}

const (
	groupHelp = "grupo local com acesso ao socket (vazio = só administradores)"
	stopHint  = "sc.exe stop blockd"
)

func defaultStateDir() string {
	pd := os.Getenv("ProgramData")
	if pd == "" {
		pd = `C:\ProgramData`
	}
	return filepath.Join(pd, "blockd")
}

func checkPrivileges() error {
	if !winsys.IsElevated() {
		return errors.New("blockd precisa rodar como administrador ou como serviço (ou use --dry-run)")
	}
	return nil
}

// wireSystem: WFP filters, NRPT + built-in sinkhole, jail by program path.
func wireSystem(o options, d *engine.Deps, log *slog.Logger) error {
	sess := wfp.NewSession(filepath.Join(o.stateDir, "wfp.json"), log)
	d.Network = wfp.NewBlocker(sess)
	d.Procs = winproc.New(winproc.WindowsSystem{}, wfp.NewJail(sess), log)
	d.DNS = nrpt.New(nrpt.Config{SinkholeAddr: o.sinkhole}, log)
	d.Kicker = winkick.New()
	d.Resolver = &resolver.Upstream{Servers: winsys.DNSServers, Fallback: []string{"9.9.9.9", "1.1.1.1"}}
	return nil
}

func newKicker(*slog.Logger) backend.SessionKicker { return winkick.New() }

// maybeService runs the daemon under the Service Control Manager when that
// is who started us. Logs go to blockd.log in the state directory.
func maybeService(o options) (bool, int) {
	isSvc, err := svc.IsWindowsService()
	if err != nil || !isSvc {
		return false, 0
	}
	w := openLog(filepath.Join(o.stateDir, "blockd.log"))
	defer w.Close()
	h := &service{o: o, log: newLogger(w, o.verbose)}
	if err := svc.Run(serviceName, h); err != nil {
		h.log.Error("serviço", "err", err)
		return true, 1
	}
	return true, int(h.code)
}

// openLog appends to path, starting over once it passes 10 MiB.
func openLog(path string) io.WriteCloser {
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	flags := os.O_CREATE | os.O_WRONLY | os.O_APPEND
	if st, err := os.Stat(path); err == nil && st.Size() > 10<<20 {
		flags |= os.O_TRUNC
	}
	f, err := os.OpenFile(path, flags, 0o644)
	if err != nil {
		return nopCloser{io.Discard}
	}
	return f
}

type nopCloser struct{ io.Writer }

func (nopCloser) Close() error { return nil }

type service struct {
	o    options
	log  *slog.Logger
	code uint32
}

func (s *service) Execute(_ []string, req <-chan svc.ChangeRequest, status chan<- svc.Status) (bool, uint32) {
	status <- svc.Status{State: svc.StartPending}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- run(ctx, s.o, s.log) }()
	status <- svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown}
	for {
		select {
		case err := <-done:
			if err != nil {
				s.log.Error("blockd", "err", err)
				s.code = 1
				return false, 1
			}
			return false, 0
		case c := <-req:
			switch c.Cmd {
			case svc.Interrogate:
				status <- c.CurrentStatus
			case svc.Stop, svc.Shutdown:
				status <- svc.Status{State: svc.StopPending}
				cancel()
				select {
				case <-done:
				case <-time.After(20 * time.Second):
					s.log.Warn("o daemon demorou para parar; saindo assim mesmo")
				}
				return false, 0
			}
		}
	}
}
