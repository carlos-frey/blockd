//go:build windows

package gui

import (
	"os/exec"
	"syscall"

	"golang.org/x/sys/windows"
)

const exeSuffix = ".exe"

// offlineHints are shown when the daemon cannot be reached.
var offlineHints = []string{
	"sc.exe start blockd   (num terminal como administrador)",
	"net localgroup blockd SEU_USUARIO /add   (depois saia e entre na sessão)",
	"blockctl status   — para ver o erro no terminal",
}

// hideConsole keeps each blockctl call from flashing a console window: the
// GUI is built as a windowsgui binary and polls blockctl constantly.
func hideConsole(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}
}
