//go:build !windows

package gui

import "os/exec"

const exeSuffix = ""

// offlineHints are shown when the daemon cannot be reached.
var offlineHints = []string{
	"sudo systemctl enable --now blockd",
	"sudo gpasswd -a $USER blockd   (depois faça logout/login)",
	"blockctl status   — para ver o erro no terminal",
}

func hideConsole(*exec.Cmd) {}
