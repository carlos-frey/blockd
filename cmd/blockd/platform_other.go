//go:build !linux && !windows

package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"

	"github.com/carlos-frey/blockd/internal/backend"
	"github.com/carlos-frey/blockd/internal/engine"
)

type platformOptions struct{}

func (p *platformOptions) register(*flag.FlagSet) {}

const (
	groupHelp = "grupo com acesso ao socket (vazio = só root)"
	stopHint  = "pare o processo do blockd"
)

func defaultStateDir() string { return "/var/lib/blockd" }

var errUnsupported = errors.New("este sistema ainda não tem backends de bloqueio; use --dry-run")

func checkPrivileges() error { return nil }

func wireSystem(options, *engine.Deps, *slog.Logger) error { return errUnsupported }

type noKicker struct{}

func (noKicker) Kick(context.Context) error { return errUnsupported }

func newKicker(*slog.Logger) backend.SessionKicker { return noKicker{} }

func maybeService(options) (bool, int) { return false, 0 }
