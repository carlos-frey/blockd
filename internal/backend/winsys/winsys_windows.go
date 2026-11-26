//go:build windows

// Package winsys wraps the few Win32 calls the Windows backends need:
// process snapshots, the IPv4 TCP table, the DNS client cache and the DNS
// servers configured on the network adapters.
package winsys

import (
	"encoding/binary"
	"errors"
	"fmt"
	"net/netip"
	"slices"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	iphlpapi                 = windows.NewLazySystemDLL("iphlpapi.dll")
	dnsapi                   = windows.NewLazySystemDLL("dnsapi.dll")
	procGetExtendedTcpTable  = iphlpapi.NewProc("GetExtendedTcpTable")
	procSetTcpEntry          = iphlpapi.NewProc("SetTcpEntry")
	procDnsFlushResolverCach = dnsapi.NewProc("DnsFlushResolverCache")
)

// IsElevated reports whether the process runs with an elevated token
// (administrator after UAC, or a service account like LocalSystem).
func IsElevated() bool { return windows.GetCurrentProcessToken().IsElevated() }

// ── Processes ──────────────────────────────────────────────────────────

// Process is one entry of a process snapshot.
type Process struct {
	PID, PPID int
	Name      string // image name as listed by the snapshot, e.g. "Discord.exe"
	Path      string // full image path; "" when the process cannot be opened
	Created   int64  // creation time (100ns ticks); 0 when unknown
}

// Processes takes a snapshot of every running process.
func Processes() (map[int]Process, error) {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil, fmt.Errorf("snapshot de processos: %w", err)
	}
	defer windows.CloseHandle(snap)

	out := map[int]Process{}
	var e windows.ProcessEntry32
	e.Size = uint32(unsafe.Sizeof(e))
	for err = windows.Process32First(snap, &e); err == nil; err = windows.Process32Next(snap, &e) {
		p := Process{
			PID:  int(e.ProcessID),
			PPID: int(e.ParentProcessID),
			Name: windows.UTF16ToString(e.ExeFile[:]),
		}
		p.Path, p.Created = details(e.ProcessID)
		out[p.PID] = p
	}
	if !errors.Is(err, windows.ERROR_NO_MORE_FILES) {
		return nil, fmt.Errorf("snapshot de processos: %w", err)
	}
	return out, nil
}

func details(pid uint32) (string, int64) {
	if pid == 0 {
		return "", 0
	}
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return "", 0
	}
	defer windows.CloseHandle(h)
	var path string
	buf := make([]uint16, windows.MAX_LONG_PATH)
	n := uint32(len(buf))
	if err := windows.QueryFullProcessImageName(h, 0, &buf[0], &n); err == nil {
		path = windows.UTF16ToString(buf[:n])
	}
	var created, exited, kernel, user windows.Filetime
	var ticks int64
	if err := windows.GetProcessTimes(h, &created, &exited, &kernel, &user); err == nil {
		ticks = int64(created.HighDateTime)<<32 | int64(created.LowDateTime)
	}
	return path, ticks
}

// ── TCP connections (IPv4) ─────────────────────────────────────────────

// TCPConn is a row of the IPv4 TCP table. Windows has no call to reset an
// IPv6 connection, so only IPv4 is listed.
type TCPConn struct {
	PID    int
	Local  netip.AddrPort
	Remote netip.AddrPort
	row    [5]uint32 // state, local addr, local port, remote addr, remote port, as stored
}

const (
	afInet                     = 2
	tcpTableOwnerPIDConnection = 4  // TCP_TABLE_OWNER_PID_CONNECTIONS
	mibTCPStateDeleteTCB       = 12 // MIB_TCP_STATE_DELETE_TCB
)

// TCPConns lists the connected IPv4 TCP sockets with their owning process.
func TCPConns() ([]TCPConn, error) {
	size := uint32(64 << 10)
	var buf []byte
	for range 8 {
		buf = make([]byte, size)
		r, _, _ := procGetExtendedTcpTable.Call(
			uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&size)), 0,
			afInet, tcpTableOwnerPIDConnection, 0)
		if r == 0 {
			return parseTCPTable(buf), nil
		}
		if windows.Errno(r) != windows.ERROR_INSUFFICIENT_BUFFER {
			return nil, fmt.Errorf("GetExtendedTcpTable: %w", windows.Errno(r))
		}
		size += 16 << 10 // the table may grow between calls
	}
	return nil, errors.New("GetExtendedTcpTable: tabela não para de crescer")
}

// parseTCPTable decodes MIB_TCPTABLE_OWNER_PID: a DWORD count followed by
// rows of six DWORDs. Addresses are in network order in memory, ports in
// network order in the low 16 bits.
func parseTCPTable(b []byte) []TCPConn {
	if len(b) < 4 {
		return nil
	}
	n := int(binary.LittleEndian.Uint32(b))
	var out []TCPConn
	for i := range n {
		off := 4 + i*24
		if off+24 > len(b) {
			break
		}
		dw := func(k int) uint32 { return binary.LittleEndian.Uint32(b[off+4*k:]) }
		c := TCPConn{PID: int(dw(5))}
		for k := range 5 {
			c.row[k] = dw(k)
		}
		c.Local = netip.AddrPortFrom(netip.AddrFrom4([4]byte(b[off+4:off+8])), port(dw(2)))
		c.Remote = netip.AddrPortFrom(netip.AddrFrom4([4]byte(b[off+12:off+16])), port(dw(4)))
		out = append(out, c)
	}
	return out
}

func port(dw uint32) uint16 { return uint16(dw&0xff)<<8 | uint16(dw>>8&0xff) }

// KillTCP resets a connection (SetTcpEntry with DELETE_TCB). Needs admin.
func KillTCP(c TCPConn) error {
	row := c.row
	row[0] = mibTCPStateDeleteTCB
	r, _, _ := procSetTcpEntry.Call(uintptr(unsafe.Pointer(&row[0])))
	if r != 0 {
		return fmt.Errorf("SetTcpEntry %s → %s: %w", c.Local, c.Remote, windows.Errno(r))
	}
	return nil
}

// KillTCPOf resets every IPv4 TCP connection owned by one of pids.
func KillTCPOf(pids []int) error {
	if len(pids) == 0 {
		return nil
	}
	conns, err := TCPConns()
	if err != nil {
		return err
	}
	var errs []error
	for _, c := range conns {
		if slices.Contains(pids, c.PID) {
			errs = append(errs, KillTCP(c))
		}
	}
	return errors.Join(errs...)
}

// ── DNS ────────────────────────────────────────────────────────────────

// FlushDNSCache empties the DNS client cache (ipconfig /flushdns).
func FlushDNSCache() error {
	if err := procDnsFlushResolverCach.Find(); err != nil {
		return err
	}
	r, _, err := procDnsFlushResolverCach.Call()
	if r == 0 {
		return fmt.Errorf("DnsFlushResolverCache: %w", err)
	}
	return nil
}

// DNSServers lists the DNS servers of the adapters that are up. NRPT rules
// do not change these, so they are the real upstream.
func DNSServers() []string {
	size := uint32(16 << 10)
	var buf []byte
	for range 4 {
		buf = make([]byte, size)
		aa := (*windows.IpAdapterAddresses)(unsafe.Pointer(&buf[0]))
		err := windows.GetAdaptersAddresses(windows.AF_UNSPEC,
			windows.GAA_FLAG_SKIP_ANYCAST|windows.GAA_FLAG_SKIP_MULTICAST|windows.GAA_FLAG_SKIP_UNICAST,
			0, aa, &size)
		if errors.Is(err, windows.ERROR_BUFFER_OVERFLOW) {
			continue
		}
		if err != nil {
			return nil
		}
		var out []string
		for ; aa != nil; aa = aa.Next {
			if aa.OperStatus != windows.IfOperStatusUp {
				continue
			}
			for d := aa.FirstDnsServerAddress; d != nil; d = d.Next {
				ip := d.Address.IP()
				a, ok := netip.AddrFromSlice(ip)
				if !ok {
					continue
				}
				a = a.Unmap()
				// Link-local needs a zone; fec0:0:0:ffff::/64 are placeholders.
				if a.IsLoopback() || a.IsLinkLocalUnicast() || strings.HasPrefix(a.String(), "fec0:") {
					continue
				}
				if s := a.String(); !slices.Contains(out, s) {
					out = append(out, s)
				}
			}
		}
		return out
	}
	return nil
}
