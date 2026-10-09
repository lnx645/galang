//go:build windows

package gne

import (
	"fmt"
	"io"
	"net"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

// gneTLSConn takes over an already-connected TCP socket on Windows.
//
// Go 1.19's net.FileConn returns EWINDOWS on this platform, so the
// connection is implemented directly over WSARecv/WSASend in blocking
// mode — the same strategy net/internal/poll uses internally. The
// handle is duplicated first so the extension keeps its own copy
// (contract: it closes its fd after a successful wrap).
func gneTLSConn(fd uintptr) (net.Conn, error) {
	self, perr := syscall.GetCurrentProcess()
	if perr != nil {
		return nil, fmt.Errorf("socket dup: %v", perr)
	}
	var dup syscall.Handle
	if err := syscall.DuplicateHandle(self, syscall.Handle(fd), self, &dup,
		0, false, syscall.DUPLICATE_SAME_ACCESS); err != nil {
		return nil, fmt.Errorf("socket dup: %v", err)
	}
	return &winSockConn{h: dup}, nil
}

// winSockConn is a synchronous net.Conn over a Winsock socket handle.
// Timeouts ride on SO_RCVTIMEO/SO_SNDTIMEO, which apply to every
// duplicate of the socket — including the extension's original fd.
type winSockConn struct {
	h    syscall.Handle
	once sync.Once
}

func (c *winSockConn) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	buf := syscall.WSABuf{Buf: &p[0], Len: uint32(len(p))}
	var n, flags uint32
	if err := syscall.WSARecv(c.h, &buf, 1, &n, &flags, nil, nil); err != nil {
		return 0, winSockErr(err)
	}
	if n == 0 {
		return 0, io.EOF // graceful close on a stream socket
	}
	return int(n), nil
}

func (c *winSockConn) Write(p []byte) (int, error) {
	total := 0
	for total < len(p) {
		buf := syscall.WSABuf{Buf: &p[total], Len: uint32(len(p) - total)}
		var n, flags uint32
		if err := syscall.WSASend(c.h, &buf, 1, &n, flags, nil, nil); err != nil {
			return total, winSockErr(err)
		}
		if n == 0 {
			return total, fmt.Errorf("short write on socket")
		}
		total += int(n)
	}
	return total, nil
}

func (c *winSockConn) Close() error {
	var err error
	c.once.Do(func() { err = syscall.Closesocket(c.h) })
	return err
}

func (c *winSockConn) LocalAddr() net.Addr                { return gneWinAddr{} }
func (c *winSockConn) RemoteAddr() net.Addr               { return gneWinAddr{} }
func (c *winSockConn) SetDeadline(t time.Time) error      { return c.setDeadlines(t, t) }
func (c *winSockConn) SetReadDeadline(t time.Time) error  { return c.applyTimeout(soRecvTimeo, t) }
func (c *winSockConn) SetWriteDeadline(t time.Time) error { return c.applyTimeout(soSndTimeo, t) }

func (c *winSockConn) setDeadlines(read, write time.Time) error {
	if err := c.applyTimeout(soRecvTimeo, read); err != nil {
		return err
	}
	return c.applyTimeout(soSndTimeo, write)
}

const (
	soSndTimeo   = 0x1005 // SO_SNDTIMEO (Windows numbering)
	soRecvTimeo  = 0x1006 // SO_RCVTIMEO
	solSocketWin = 0xffff // SOL_SOCKET
)

var procSetSockOpt = syscall.NewLazyDLL("ws2_32.dll").NewProc("setsockopt")

// applyTimeout mirrors net.Conn deadlines onto the socket options; a
// zero time clears the timeout (0 µs), a past deadline clamps to the
// smallest non-zero value so it still fires.
func (c *winSockConn) applyTimeout(opt int, t time.Time) error {
	var d time.Duration
	if !t.IsZero() {
		d = time.Until(t)
		if d <= 0 {
			d = time.Microsecond
		}
	}
	tv := syscall.Timeval{
		Sec:  int32(d / time.Second),
		Usec: int32((d % time.Second) / time.Microsecond),
	}
	r1, _, e1 := procSetSockOpt.Call(
		uintptr(c.h),
		uintptr(solSocketWin),
		uintptr(opt),
		uintptr(unsafe.Pointer(&tv)),
		unsafe.Sizeof(tv),
	)
	if r1 != 0 {
		if e1 != nil && e1 != syscall.Errno(0) {
			return e1
		}
		return fmt.Errorf("setsockopt(SO_*TIMEO) failed")
	}
	return nil
}

// Winsock error codes the syscall package does not name on this Go
// version (WSAETIMEDOUT = 10060, WSAEWOULDBLOCK = 10035).
const (
	wsaETimedOut   = syscall.Errno(10060)
	wsaEWouldBlock = syscall.Errno(10035)
)

// winSockErr maps the timeout errors to a value implementing net.Error
// with Timeout() == true, which is how tls_classify distinguishes
// GNE_TLS_TIMEOUT from GNE_TLS_ERR.
func winSockErr(err error) error {
	if err == wsaETimedOut || err == wsaEWouldBlock {
		return gneWinTimeout{}
	}
	return err
}

type gneWinTimeout struct{}

func (gneWinTimeout) Error() string   { return "i/o timeout" }
func (gneWinTimeout) Timeout() bool   { return true }
func (gneWinTimeout) Temporary() bool { return true }

type gneWinAddr struct{}

func (gneWinAddr) Network() string { return "tcp" }
func (gneWinAddr) String() string  { return "tls-session" }
