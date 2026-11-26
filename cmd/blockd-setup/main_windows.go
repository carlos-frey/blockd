//go:build windows

// blockd-setup is the Windows installer: one exe that carries blockd.exe,
// blockctl.exe (and blockd-gui.exe, when built) plus the PowerShell scripts in
// packaging/windows, and runs them elevated. Build it with `make windows-setup`.
//
//	blockd-setup.exe              asks what to do (install, update or remove)
//	blockd-setup.exe -install     installs or updates
//	blockd-setup.exe -uninstall   removes (what "Apps" in Settings runs)
//
// The install copies the setup itself to %ProgramFiles%\blockd and registers
// it as the uninstaller in Settings > Apps.
package main

import (
	"bufio"
	"embed"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"golang.org/x/sys/windows"
)

//go:embed payload
var payload embed.FS

// version is set by the Makefile (git describe).
var version = "dev"

const selfName = "blockd-setup.exe"

func main() {
	install := flag.Bool("install", false, "instala ou atualiza sem perguntar")
	uninstall := flag.Bool("uninstall", false, "remove o blockd sem perguntar")
	user := flag.String("user", os.Getenv("USERNAME"), "usuário que entra no grupo local blockd")
	quiet := flag.Bool("quiet", false, "não espera Enter no fim")
	flag.Parse()

	fmt.Printf("blockd %s — instalador para Windows\n\n", version)
	code, wait := run(*install, *uninstall, *user)
	if wait && !*quiet {
		fmt.Print("\nPressione Enter para fechar.")
		_, _ = bufio.NewReader(os.Stdin).ReadString('\n')
	}
	os.Exit(code)
}

// run returns the exit code and whether this window should wait for Enter
// (not when the work goes on in another process).
func run(install, uninstall bool, user string) (int, bool) {
	if install && uninstall {
		fmt.Println("Use -install ou -uninstall, não os dois.")
		return 2, true
	}
	if !install && !uninstall {
		switch ask() {
		case 'i':
			install = true
		case 'r':
			uninstall = true
		default:
			return 0, false
		}
	}
	action := "-install"
	if uninstall {
		action = "-uninstall"
	}

	self, err := os.Executable()
	if err != nil {
		fmt.Println("Não achei o próprio executável:", err)
		return 1, true
	}

	if !windows.GetCurrentProcessToken().IsElevated() {
		// The elevated copy opens its own window; this one just goes away.
		args := []string{action, "-user", user}
		if err := runElevated(self, args); err != nil {
			if errors.Is(err, windows.ERROR_CANCELLED) {
				fmt.Println("Cancelado: o instalador precisa de permissão de administrador.")
			} else {
				fmt.Println("Não consegui pedir permissão de administrador:", err)
			}
			return 1, true
		}
		return 0, false
	}

	// The scripts replace or delete %ProgramFiles%\blockd, and Windows won't
	// touch a running exe: when we are the installed copy, hand over to a
	// temp copy.
	if sameDir(filepath.Dir(self), installDir()) {
		if err := relaunchFromTemp(self, []string{action, "-user", user}); err != nil {
			fmt.Println("Não consegui continuar:", err)
			return 1, true
		}
		return 0, false
	}

	script := "install.ps1"
	if uninstall {
		script = "uninstall.ps1"
	}
	if err := runScript(self, script, user); err != nil {
		fmt.Println("\nFalhou:", err)
		return 1, true
	}
	return 0, true
}

func installDir() string {
	pf := os.Getenv("ProgramFiles")
	if pf == "" {
		pf = `C:\Program Files`
	}
	return filepath.Join(pf, "blockd")
}

func installed() bool {
	_, err := os.Stat(filepath.Join(installDir(), "blockd.exe"))
	return err == nil
}

// ask returns 'i' (install/update), 'r' (remove) or 0 (leave).
func ask() byte {
	in := bufio.NewReader(os.Stdin)
	if !installed() {
		fmt.Println("Isto instala o serviço blockd, o blockctl e o grupo local blockd.")
		fmt.Print("Instalar agora? [S/n] ")
		line, _ := in.ReadString('\n')
		switch strings.ToLower(strings.TrimSpace(line)) {
		case "", "s", "sim", "y", "yes":
			return 'i'
		}
		return 0
	}
	fmt.Println("O blockd já está instalado.")
	fmt.Println("  [A] atualizar")
	fmt.Println("  [R] remover")
	fmt.Println("  [S] sair")
	for {
		fmt.Print("> ")
		line, err := in.ReadString('\n')
		switch strings.ToLower(strings.TrimSpace(line)) {
		case "a", "atualizar":
			return 'i'
		case "r", "remover":
			return 'r'
		case "s", "sair":
			return 0
		}
		if err != nil {
			return 0
		}
	}
}

// runElevated starts exe with args through UAC ("runas") and returns once
// the elevated process has started.
func runElevated(exe string, args []string) error {
	verb, _ := windows.UTF16PtrFromString("runas")
	file, err := windows.UTF16PtrFromString(exe)
	if err != nil {
		return err
	}
	params, err := windows.UTF16PtrFromString(joinArgs(args))
	if err != nil {
		return err
	}
	cwd, _ := windows.UTF16PtrFromString(filepath.Dir(exe))
	return windows.ShellExecute(0, verb, file, params, cwd, windows.SW_SHOWNORMAL)
}

func joinArgs(args []string) string {
	q := make([]string, len(args))
	for i, a := range args {
		q[i] = syscall.EscapeArg(a)
	}
	return strings.Join(q, " ")
}

// relaunchFromTemp copies exe to %TEMP% and starts the copy in this console,
// without waiting. The copy is deleted on the next reboot.
func relaunchFromTemp(exe string, args []string) error {
	dir, err := os.MkdirTemp("", "blockd-setup-")
	if err != nil {
		return err
	}
	tmp := filepath.Join(dir, selfName)
	if err := copyFile(exe, tmp); err != nil {
		return err
	}
	cmd := exec.Command(tmp, args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Start(); err != nil {
		return err
	}
	for _, p := range []string{tmp, dir} {
		if p16, err := windows.UTF16PtrFromString(p); err == nil {
			_ = windows.MoveFileEx(p16, nil, windows.MOVEFILE_DELAY_UNTIL_REBOOT)
		}
	}
	return nil
}

// runScript unpacks the payload (and, for the install, this exe) to a temp
// directory and runs script from there with Windows PowerShell.
func runScript(self, script, user string) error {
	dir, err := os.MkdirTemp("", "blockd-setup-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)

	if err := unpack(dir); err != nil {
		return fmt.Errorf("extraindo os arquivos: %w", err)
	}
	if _, err := os.Stat(filepath.Join(dir, script)); err != nil {
		return fmt.Errorf("este instalador foi compilado sem %s (use `make windows-setup`)", script)
	}
	args := []string{"-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass",
		"-File", filepath.Join(dir, script)}
	if script == "install.ps1" {
		if _, err := os.Stat(filepath.Join(dir, "blockd.exe")); err != nil {
			return errors.New("este instalador foi compilado sem blockd.exe (use `make windows-setup`)")
		}
		if err := copyFile(self, filepath.Join(dir, selfName)); err != nil {
			return err
		}
		args = append(args, "-Source", dir, "-Version", version)
		if user != "" {
			args = append(args, "-User", user)
		}
	}

	cmd := exec.Command(powershell(), args...)
	cmd.Dir = dir
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s: %w", script, err)
	}
	return nil
}

// powershell is Windows PowerShell by full path, so nothing on PATH can
// stand in for it while we run elevated.
func powershell() string {
	root := os.Getenv("SystemRoot")
	if root == "" {
		root = `C:\Windows`
	}
	return filepath.Join(root, "System32", "WindowsPowerShell", "v1.0", "powershell.exe")
}

func unpack(dir string) error {
	return fs.WalkDir(payload, "payload", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		name := d.Name()
		if !strings.HasSuffix(name, ".exe") && !strings.HasSuffix(name, ".ps1") {
			return nil
		}
		src, err := payload.Open(p)
		if err != nil {
			return err
		}
		defer src.Close()
		return writeFile(filepath.Join(dir, name), src)
	})
}

func copyFile(from, to string) error {
	src, err := os.Open(from)
	if err != nil {
		return err
	}
	defer src.Close()
	return writeFile(to, src)
}

func writeFile(path string, r io.Reader) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, r); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

func sameDir(a, b string) bool {
	return strings.EqualFold(filepath.Clean(a), filepath.Clean(b))
}
