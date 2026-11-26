//go:build windows

package ipc

import (
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/windows"
)

// DefaultSocket lives in its own directory so its ACL can be set before the
// socket exists: the socket file inherits it.
var DefaultSocket = filepath.Join(programData(), "blockd", "run", "blockd.sock")

func programData() string {
	if p := os.Getenv("ProgramData"); p != "" {
		return p
	}
	return `C:\ProgramData`
}

const (
	hintStatus     = "(sc.exe query blockd)"
	hintNotRunning = "(o serviço está rodando? sc.exe query blockd)"
	hintPermission = "(seu usuário está no grupo local blockd? saia e entre de novo na sessão depois de adicionar)"
)

// prepareSocket gives the socket directory a protected ACL: full control for
// SYSTEM and Administrators, read/write for the group. AF_UNIX on Windows
// checks write access on the socket file to connect, and the file inherits
// this ACL when the daemon binds it.
func prepareSocket(path, group string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	sddl := "D:P(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)"
	if group != "" {
		sid, _, _, err := windows.LookupSID("", group)
		if err != nil {
			return fmt.Errorf("grupo %q: %w (crie com: net localgroup %s /add)", group, err, group)
		}
		sddl += fmt.Sprintf("(A;OICI;FRFWFX;;;%s)", sid.String())
	}
	sd, err := windows.SecurityDescriptorFromString(sddl)
	if err != nil {
		return err
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		return err
	}
	err = windows.SetNamedSecurityInfo(dir, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, dacl, nil)
	if err != nil {
		return fmt.Errorf("ACL de %s: %w", dir, err)
	}
	return nil
}

func secureSocket(string, string) error { return nil }
