//go:build windows

package winproc

import "github.com/carlos-frey/blockd/internal/backend/winsys"

// WindowsSystem reads processes with a Toolhelp snapshot and resets IPv4
// TCP connections with SetTcpEntry.
type WindowsSystem struct{}

func (WindowsSystem) Procs() (map[int]Proc, error) {
	ps, err := winsys.Processes()
	if err != nil {
		return nil, err
	}
	out := make(map[int]Proc, len(ps))
	for pid, p := range ps {
		out[pid] = Proc{PID: p.PID, PPID: p.PPID, Name: p.Name, Path: p.Path, Created: p.Created}
	}
	return out, nil
}

func (WindowsSystem) KillConns(pids []int) error { return winsys.KillTCPOf(pids) }
