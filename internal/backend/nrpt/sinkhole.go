package nrpt

import (
	"encoding/binary"
	"errors"
	"log/slog"
	"net"
	"sync"
	"time"
)

// Sinkhole is a tiny DNS server that answers NXDOMAIN to every query. Only
// blocked names are routed to it, so it never needs to know which ones.
type Sinkhole struct {
	Addr string // host:port
	Log  *slog.Logger

	mu   sync.Mutex
	conn net.PacketConn
}

// Start binds the socket if it is not bound yet.
func (s *Sinkhole) Start() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.conn != nil {
		return nil
	}
	c, err := net.ListenPacket("udp", s.Addr)
	if err != nil {
		return err
	}
	s.conn = c
	go s.serve(c)
	return nil
}

// Running reports whether the socket is bound and being served.
func (s *Sinkhole) Running() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.conn != nil
}

func (s *Sinkhole) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.conn != nil {
		s.conn.Close()
		s.conn = nil
	}
}

func (s *Sinkhole) serve(c net.PacketConn) {
	buf := make([]byte, 4096)
	for {
		n, addr, err := c.ReadFrom(buf)
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return
			}
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() {
				continue
			}
			// Windows reports ICMP port unreachable from an earlier reply as
			// a read error (WSAECONNRESET) on UDP sockets; keep serving.
			if s.Log != nil {
				s.Log.Debug("sinkhole: erro de leitura", "err", err)
			}
			time.Sleep(10 * time.Millisecond) // never spin on a persistent error
			continue
		}
		if resp := NXDomain(buf[:n]); resp != nil {
			_, _ = c.WriteTo(resp, addr)
		}
	}
}

// DNS header flags.
const (
	flagQR     = 0x8000
	maskOpcode = 0x7800
	flagRD     = 0x0100
	flagRA     = 0x0080

	rcodeFormErr  = 1
	rcodeNXDomain = 3
	rcodeNotImp   = 4
)

// NXDomain builds the answer to query: the same ID and question, no records,
// rcode NXDOMAIN. Malformed queries get FORMERR, other opcodes NOTIMP, and
// responses (QR set) are ignored (nil).
func NXDomain(q []byte) []byte {
	if len(q) < 12 {
		return nil
	}
	flags := binary.BigEndian.Uint16(q[2:4])
	if flags&flagQR != 0 {
		return nil
	}
	end, qd, rcode := 12, uint16(0), uint16(rcodeFormErr)
	switch {
	case flags&maskOpcode != 0:
		rcode = rcodeNotImp
	case binary.BigEndian.Uint16(q[4:6]) >= 1:
		if e, ok := questionEnd(q, 12); ok {
			end, qd, rcode = e, 1, rcodeNXDomain
		}
	}
	resp := make([]byte, end)
	copy(resp, q[:end])
	binary.BigEndian.PutUint16(resp[2:4], flagQR|flags&(maskOpcode|flagRD)|flagRA|rcode)
	binary.BigEndian.PutUint16(resp[4:6], qd)
	clear(resp[6:12]) // AN, NS, AR counts
	return resp
}

// questionEnd returns where the question starting at off ends: the name as
// uncompressed labels, then QTYPE and QCLASS.
func questionEnd(q []byte, off int) (int, bool) {
	for i := off; i < len(q); {
		l := int(q[i])
		switch {
		case l == 0:
			if i+5 > len(q) {
				return 0, false
			}
			return i + 5, true
		case l&0xc0 != 0:
			return 0, false // compression has no place in a query's question
		default:
			i += 1 + l
		}
	}
	return 0, false
}
