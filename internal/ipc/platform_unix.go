//go:build !windows

package ipc

import (
	"fmt"
	"os"
	"os/user"
	"strconv"
)

// DefaultSocket is where the daemon listens unless told otherwise.
var DefaultSocket = "/run/blockd/blockd.sock"

const (
	hintStatus     = "(systemctl status blockd)"
	hintNotRunning = "(o serviço está rodando? systemctl status blockd)"
	hintPermission = "(seu usuário está no grupo blockd? faça logout/login depois de adicionar)"
)

func prepareSocket(string, string) error { return nil }

// secureSocket leaves the socket to root and the group, mode 0660.
func secureSocket(path, group string) error {
	if err := os.Chmod(path, 0o660); err != nil {
		return err
	}
	if group == "" {
		return nil
	}
	g, err := user.LookupGroup(group)
	if err != nil {
		return fmt.Errorf("grupo %q: %w", group, err)
	}
	gid, _ := strconv.Atoi(g.Gid)
	return os.Chown(path, 0, gid)
}
