//go:build cgo

package gne

/*
#cgo CFLAGS: -I${SRCDIR}/../../../include
#include "shim.h"
#include <stdlib.h>
#include <string.h>
*/
import "C"

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"net"
	"os"
	"sync"
	"time"
	"unsafe"

	"galang/internal/domain"
)

// ABI 3: client-side TLS. The extension keeps its own TCP socket for
// plain traffic; tls_wrap takes an already-connected socket, dups it
// and performs the handshake through crypto/tls, after which all I/O
// goes through tls_read/tls_write. Entries live in a package-level map
// keyed by handle id — ids are monotonic (never reused), so a lookup
// can only ever find the connection that tls_wrap created.
const (
	gneTLSErr     = -1 // mirrors GNE_TLS_ERR in include/gne.h
	gneTLSTimeout = -2 // mirrors GNE_TLS_TIMEOUT in include/gne.h
	gneTLSDefault = 30 * time.Second
)

var (
	tlsmu    sync.Mutex
	tlsConns = map[uint64]net.Conn{}
)

func tlsStore(h uint64, c net.Conn) {
	tlsmu.Lock()
	tlsConns[h] = c
	tlsmu.Unlock()
}

func tlsFetch(h uint64) net.Conn {
	tlsmu.Lock()
	c := tlsConns[h]
	tlsmu.Unlock()
	return c
}

func tlsDrop(h uint64) net.Conn {
	tlsmu.Lock()
	c := tlsConns[h]
	delete(tlsConns, h)
	tlsmu.Unlock()
	return c
}

// tlsTimeout resolves the documented timeout rule (<= 0 → 30 s).
func tlsTimeout(ms C.int32_t) time.Duration {
	if ms > 0 {
		return time.Duration(ms) * time.Millisecond
	}
	return gneTLSDefault
}

// tlsWriteErr copies a short English reason into the caller's buffer,
// always NUL-terminated and truncated to fit.
func tlsWriteErr(buf *C.char, cap C.size_t, msg string) {
	if buf == nil || cap == 0 {
		return
	}
	max := int(cap) - 1
	if len(msg) > max {
		msg = msg[:max]
	}
	if len(msg) > 0 {
		b := []byte(msg)
		C.memcpy(unsafe.Pointer(buf), unsafe.Pointer(&b[0]), C.size_t(len(b)))
	}
	*(*C.char)(unsafe.Pointer(uintptr(unsafe.Pointer(buf)) + uintptr(len(msg)))) = 0
}

// tlsLoadRoots builds the verification pool: system roots, plus the
// optional extra CA bundle in PEM form.
func tlsLoadRoots(ca string) (*x509.CertPool, error) {
	pool, err := x509.SystemCertPool()
	if err != nil || pool == nil {
		pool = x509.NewCertPool()
	}
	pem, err := os.ReadFile(ca)
	if err != nil {
		return nil, fmt.Errorf("cannot read ca_file %q: %v", ca, err)
	}
	if !pool.AppendCertsFromPEM(pem) {
		return nil, fmt.Errorf("ca_file %q contains no certificates", ca)
	}
	return pool, nil
}

// gne_host_tls_wrap dups the connected socket and performs the TLS
// handshake; server_name is used as SNI and as the verification host.
// On success *out receives a fresh object handle (the extension must
// retain() it before storing it past this call); on failure *out stays
// 0 and errbuf carries a short English reason. Never throws.
//
//export gne_host_tls_wrap
func gne_host_tls_wrap(ctx *C.gne_ctx, fd C.uintptr_t, serverName, caFile *C.char,
	flags, timeoutMs C.int32_t, errbuf *C.char, errcap C.size_t,
	out *C.uint64_t) C.int {
	return gneGuardI(ctx, func() C.int {
		m := modFor(ctx)
		if m == nil || out == nil {
			return C.int(gneTLSErr)
		}
		*out = 0
		if fd == 0 {
			tlsWriteErr(errbuf, errcap, "invalid socket")
			return C.int(gneTLSErr)
		}
		sname := ""
		if serverName != nil {
			sname = C.GoString(serverName)
		}
		ca := ""
		if caFile != nil {
			ca = C.GoString(caFile)
		}
		insecure := int(flags)&int(C.GNE_TLS_INSECURE) != 0
		if sname == "" && !insecure {
			tlsWriteErr(errbuf, errcap,
				"server_name is required to verify the certificate (or pass GNE_TLS_INSECURE)")
			return C.int(gneTLSErr)
		}

		conn, err := gneTLSConn(uintptr(fd))
		if err != nil {
			tlsWriteErr(errbuf, errcap, err.Error())
			return C.int(gneTLSErr)
		}
		cfg := &tls.Config{
			ServerName:         sname,
			InsecureSkipVerify: insecure, // explicit opt-in, tests/localhost only
			MinVersion:         tls.VersionTLS12,
		}
		if ca != "" {
			pool, rerr := tlsLoadRoots(ca)
			if rerr != nil {
				conn.Close()
				tlsWriteErr(errbuf, errcap, rerr.Error())
				return C.int(gneTLSErr)
			}
			cfg.RootCAs = pool
		}

		deadline := time.Now().Add(tlsTimeout(timeoutMs))
		conn.SetDeadline(deadline)
		tc := tls.Client(conn, cfg)
		if err := tc.Handshake(); err != nil {
			tc.Close()
			tlsWriteErr(errbuf, errcap, err.Error())
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				return C.int(gneTLSTimeout)
			}
			return C.int(gneTLSErr)
		}
		conn.SetDeadline(time.Time{})

		h := uint64(m.newTemp(domain.NewObj()))
		tlsStore(h, tc)
		*out = C.uint64_t(h)
		return 0
	})
}

// gne_host_tls_read reads up to cap bytes with a deadline. Returns 0
// with *n > 0 for data, 0 with *n == 0 when the peer closed the
// connection (recv semantics), GNE_TLS_TIMEOUT or GNE_TLS_ERR
// otherwise. Never throws.
//
//export gne_host_tls_read
func gne_host_tls_read(ctx *C.gne_ctx, h C.uint64_t, buf *C.char, cap C.size_t,
	n *C.size_t, timeoutMs C.int32_t) C.int {
	return gneGuardI(ctx, func() C.int {
		if n == nil {
			return C.int(gneTLSErr)
		}
		*n = 0
		if buf == nil || cap == 0 {
			return C.int(gneTLSErr)
		}
		c := tlsFetch(uint64(h))
		if c == nil {
			return C.int(gneTLSErr)
		}
		c.SetReadDeadline(time.Now().Add(tlsTimeout(timeoutMs)))
		b := unsafe.Slice((*byte)(unsafe.Pointer(buf)), int(cap))
		nn, err := c.Read(b)
		*n = C.size_t(nn)
		if err == nil || nn > 0 {
			return 0
		}
		if err == io.EOF {
			return 0 // *n == 0: clean close
		}
		if ne, ok := err.(net.Error); ok && ne.Timeout() {
			return C.int(gneTLSTimeout)
		}
		return C.int(gneTLSErr)
	})
}

// gne_host_tls_write writes the whole buffer with a deadline.
// Never throws.
//
//export gne_host_tls_write
func gne_host_tls_write(ctx *C.gne_ctx, h C.uint64_t, buf *C.char, n C.size_t,
	timeoutMs C.int32_t) C.int {
	return gneGuardI(ctx, func() C.int {
		if n == 0 {
			return 0
		}
		if buf == nil {
			return C.int(gneTLSErr)
		}
		c := tlsFetch(uint64(h))
		if c == nil {
			return C.int(gneTLSErr)
		}
		c.SetWriteDeadline(time.Now().Add(tlsTimeout(timeoutMs)))
		b := unsafe.Slice((*byte)(unsafe.Pointer(buf)), int(n))
		for len(b) > 0 {
			nn, err := c.Write(b)
			if err != nil {
				if ne, ok := err.(net.Error); ok && ne.Timeout() {
					return C.int(gneTLSTimeout)
				}
				return C.int(gneTLSErr)
			}
			b = b[nn:]
		}
		return 0
	})
}

// gne_host_tls_close shuts the session down and forgets the handle;
// a second close of the same handle reports GNE_TLS_ERR. Never throws.
//
//export gne_host_tls_close
func gne_host_tls_close(ctx *C.gne_ctx, h C.uint64_t) C.int {
	return gneGuardI(ctx, func() C.int {
		c := tlsDrop(uint64(h))
		if c == nil {
			return C.int(gneTLSErr)
		}
		if err := c.Close(); err != nil {
			return C.int(gneTLSErr)
		}
		return 0
	})
}
