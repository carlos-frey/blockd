// Package ipc carries api.Request/api.Response as JSON lines over the daemon's
// Unix socket.
package ipc

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/user"
	"strconv"
	"time"

	"github.com/carlos-frey/blockd/internal/api"
	"github.com/carlos-frey/blockd/internal/domain"
	"github.com/carlos-frey/blockd/internal/engine"
)

const DefaultSocket = "/run/blockd/blockd.sock"

// SocketPath honours $BLOCKD_SOCKET (handy for dry runs).
func SocketPath() string {
	if p := os.Getenv("BLOCKD_SOCKET"); p != "" {
		return p
	}
	return DefaultSocket
}

// readOnly methods are polled by the GUI and not worth a log line.
var readOnly = map[string]bool{
	api.MStatus: true, api.MCatalogList: true, api.MCatalogShow: true,
	api.MProfileList: true, api.MProfileShow: true, api.MScheduleList: true, api.MScheduleShow: true,
}

type handler func(ctx context.Context, params json.RawMessage) (any, error)

// Server dispatches requests to the engine.
type Server struct {
	h   map[string]handler
	log *slog.Logger
}

func decode[T any](raw json.RawMessage) (T, error) {
	var v T
	if len(raw) == 0 {
		return v, nil
	}
	if err := json.Unmarshal(raw, &v); err != nil {
		return v, domain.Errorf(domain.CodeInvalidArgument, "parâmetros inválidos: %v", err)
	}
	return v, nil
}

// on registers a method whose params decode into P.
func on[P any](s *Server, method string, fn func(context.Context, P) (any, error)) {
	s.h[method] = func(ctx context.Context, raw json.RawMessage) (any, error) {
		p, err := decode[P](raw)
		if err != nil {
			return nil, err
		}
		return fn(ctx, p)
	}
}

type none struct{}

func NewServer(svc *engine.Service, log *slog.Logger) *Server {
	s := &Server{h: map[string]handler{}, log: log}
	ok := func(err error) (any, error) {
		if err != nil {
			return nil, err
		}
		return svc.Status(context.Background()), nil
	}
	on(s, api.MStatus, func(ctx context.Context, _ none) (any, error) { return svc.Status(ctx), nil })
	on(s, api.MSync, func(ctx context.Context, _ none) (any, error) { return svc.Sync(ctx) })
	on(s, api.MCatalogList, func(ctx context.Context, p api.CatalogListParams) (any, error) {
		return svc.CatalogList(ctx, p), nil
	})
	on(s, api.MCatalogShow, func(ctx context.Context, p api.IDParams) (any, error) { return svc.CatalogShow(ctx, p.ID) })
	on(s, api.MCatalogAdd, func(ctx context.Context, p api.CatalogAddParams) (any, error) { return svc.CatalogAdd(ctx, p.App) })
	on(s, api.MCatalogEdit, func(ctx context.Context, p api.CatalogEditParams) (any, error) { return svc.CatalogEdit(ctx, p) })
	on(s, api.MCatalogRemove, func(ctx context.Context, p api.IDParams) (any, error) { return ok(svc.CatalogRemove(ctx, p.ID)) })
	on(s, api.MAppBlock, func(ctx context.Context, p api.IDsParams) (any, error) { return ok(svc.Block(ctx, p.IDs)) })
	on(s, api.MAppUnblock, func(ctx context.Context, p api.IDsParams) (any, error) { return ok(svc.Unblock(ctx, p.IDs)) })
	on(s, api.MProfileList, func(ctx context.Context, _ none) (any, error) { return svc.ProfileList(ctx), nil })
	on(s, api.MProfileShow, func(ctx context.Context, p api.IDParams) (any, error) { return svc.ProfileShow(ctx, p.ID) })
	on(s, api.MProfileCreate, func(ctx context.Context, p api.ProfileCreateParams) (any, error) { return svc.ProfileCreate(ctx, p) })
	on(s, api.MProfileEdit, func(ctx context.Context, p api.ProfileEditParams) (any, error) { return svc.ProfileEdit(ctx, p) })
	on(s, api.MProfileClone, func(ctx context.Context, p api.ProfileCloneParams) (any, error) { return svc.ProfileClone(ctx, p) })
	on(s, api.MProfileRemove, func(ctx context.Context, p api.IDParams) (any, error) { return ok(svc.ProfileRemove(ctx, p.ID)) })
	on(s, api.MProfileOn, func(ctx context.Context, p api.IDsParams) (any, error) { return ok(svc.ProfileOn(ctx, p.IDs)) })
	on(s, api.MProfileOff, func(ctx context.Context, p api.IDsParams) (any, error) { return ok(svc.ProfileOff(ctx, p.IDs)) })

	on(s, api.MScheduleList, func(ctx context.Context, _ none) (any, error) { return svc.ScheduleList(ctx), nil })
	on(s, api.MScheduleShow, func(ctx context.Context, p api.IDParams) (any, error) { return svc.ScheduleShow(ctx, p.ID) })
	on(s, api.MScheduleCreate, func(ctx context.Context, p api.ScheduleCreateParams) (any, error) { return svc.ScheduleCreate(ctx, p) })
	on(s, api.MScheduleEdit, func(ctx context.Context, p api.ScheduleEditParams) (any, error) { return svc.ScheduleEdit(ctx, p) })
	on(s, api.MScheduleRemove, func(ctx context.Context, p api.IDParams) (any, error) { return ok(svc.ScheduleRemove(ctx, p.ID)) })
	on(s, api.MScheduleOn, func(ctx context.Context, p api.IDsParams) (any, error) {
		return ok(svc.ScheduleEnable(ctx, p.IDs, true))
	})
	on(s, api.MScheduleOff, func(ctx context.Context, p api.IDsParams) (any, error) {
		return ok(svc.ScheduleEnable(ctx, p.IDs, false))
	})
	on(s, api.MSchedulePause, func(ctx context.Context, p api.PauseParams) (any, error) { return svc.SchedulePause(ctx, p) })
	on(s, api.MScheduleResume, func(ctx context.Context, p api.IDParams) (any, error) { return svc.ScheduleResume(ctx, p.ID) })
	on(s, api.MScheduleSkip, func(ctx context.Context, p api.ScheduleSkipParams) (any, error) { return svc.ScheduleSkip(ctx, p) })
	on(s, api.MFocusStart, func(ctx context.Context, p api.FocusStartParams) (any, error) { return svc.FocusStart(ctx, p) })
	on(s, api.MFocusStop, func(ctx context.Context, p api.PauseParams) (any, error) { return ok(svc.FocusStop(ctx, p)) })
	on(s, api.MFocusResume, func(ctx context.Context, p api.IDParams) (any, error) { return svc.FocusResume(ctx, p.ID) })
	on(s, api.MFocusExtend, func(ctx context.Context, p api.FocusExtendParams) (any, error) { return svc.FocusExtend(ctx, p) })
	return s
}

// Handle runs one request; it never fails, errors travel in the response.
func (s *Server) Handle(ctx context.Context, req api.Request) api.Response {
	h, found := s.h[req.Method]
	if !found {
		return api.Response{Error: domain.Errorf(domain.CodeInvalidArgument, "método desconhecido %q", req.Method)}
	}
	data, err := h(ctx, req.Params)
	if err != nil {
		return api.Response{Error: domain.AsError(err)}
	}
	b, err := json.Marshal(data)
	if err != nil {
		return api.Response{Error: domain.AsError(err)}
	}
	return api.Response{OK: true, Data: b}
}

func (s *Server) Serve(ctx context.Context, ln net.Listener) error {
	go func() { <-ctx.Done(); ln.Close() }()
	for {
		conn, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		go s.serveConn(ctx, conn)
	}
}

func (s *Server) serveConn(ctx context.Context, conn net.Conn) {
	defer conn.Close()
	sc := bufio.NewScanner(conn)
	sc.Buffer(make([]byte, 64*1024), 4<<20)
	enc := json.NewEncoder(conn)
	for {
		_ = conn.SetReadDeadline(time.Now().Add(time.Minute))
		if !sc.Scan() {
			return
		}
		var req api.Request
		resp := api.Response{}
		if err := json.Unmarshal(sc.Bytes(), &req); err != nil {
			resp.Error = domain.Errorf(domain.CodeInvalidArgument, "requisição inválida: %v", err)
		} else {
			resp = s.Handle(ctx, req)
			if !readOnly[req.Method] {
				s.log.Info("request", "method", req.Method, "ok", resp.OK)
			}
		}
		if err := enc.Encode(resp); err != nil {
			return
		}
	}
}

// ErrAlreadyRunning means another daemon answers on the socket.
var ErrAlreadyRunning = errors.New("já existe um blockd rodando neste socket (systemctl status blockd)")

// Listen creates the socket, readable and writable by owner and group.
// A stale socket file is replaced, but a live one is left alone: removing it
// would orphan the running daemon.
func Listen(path, group string) (net.Listener, error) {
	if conn, err := net.DialTimeout("unix", path, time.Second); err == nil {
		conn.Close()
		return nil, ErrAlreadyRunning
	}
	_ = os.Remove(path)
	ln, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(path, 0o660); err != nil {
		ln.Close()
		return nil, err
	}
	if group != "" {
		g, err := user.LookupGroup(group)
		if err != nil {
			ln.Close()
			return nil, fmt.Errorf("grupo %q: %w", group, err)
		}
		gid, _ := strconv.Atoi(g.Gid)
		if err := os.Chown(path, 0, gid); err != nil {
			ln.Close()
			return nil, err
		}
	}
	return ln, nil
}

// Client talks to the daemon; one connection per call keeps it simple.
type Client struct {
	Path    string
	Timeout time.Duration
}

func NewClient() *Client { return &Client{Path: SocketPath(), Timeout: 30 * time.Second} }

// Raw sends a request and returns the response envelope as received.
func (c *Client) Raw(ctx context.Context, method string, params any) api.Response {
	unavailable := func(err error) api.Response {
		msg := fmt.Sprintf("não consegui falar com o blockd em %s: %v", c.Path, err)
		if errors.Is(err, os.ErrPermission) {
			msg += " (seu usuário está no grupo blockd? faça logout/login depois de adicionar)"
		} else {
			msg += " (o serviço está rodando? systemctl status blockd)"
		}
		return api.Response{Error: &domain.Error{Code: domain.CodeDaemonUnavailable, Message: msg}}
	}
	d := net.Dialer{Timeout: 3 * time.Second}
	conn, err := d.DialContext(ctx, "unix", c.Path)
	if err != nil {
		return unavailable(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(c.Timeout))
	req := api.Request{Method: method}
	if params != nil {
		if req.Params, err = json.Marshal(params); err != nil {
			return api.Response{Error: domain.AsError(err)}
		}
	}
	if err := json.NewEncoder(conn).Encode(req); err != nil {
		return unavailable(err)
	}
	var resp api.Response
	if err := json.NewDecoder(conn).Decode(&resp); err != nil {
		return unavailable(err)
	}
	return resp
}

// Call decodes the data into out (when non-nil) or returns the typed error.
func (c *Client) Call(ctx context.Context, method string, params, out any) error {
	resp := c.Raw(ctx, method, params)
	if !resp.OK {
		if resp.Error == nil {
			return &domain.Error{Code: domain.CodeInternal, Message: "resposta sem erro nem dados"}
		}
		return resp.Error
	}
	if out != nil {
		return json.Unmarshal(resp.Data, out)
	}
	return nil
}
