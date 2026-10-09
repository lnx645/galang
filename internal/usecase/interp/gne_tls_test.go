//go:build cgo && !windows

package interp

// ABI 3 TLS integration: builds a C extension that dials a local
// socket and drives api->tls_wrap/tls_read/tls_write/tls_close against
// a Go TLS mock, covering the happy path (ca_file), system-roots
// rejection, insecure mode, hostname mismatch and handshake timeout.

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// fixtureTLS probes the TLS host surface: probe(host, port, ca_file,
// insecure, timeout_ms) dials 127.0.0.1, wraps the socket, sends
// "PING\n" and reads the reply. It returns {code, data, err} where
// code 0 = success, -1 = tls error, -2 = handshake timeout,
// -3 = TCP connect failed, -4 = post-handshake I/O failed.
const fixtureTLS = `#include "gne.h"
#include <stdint.h>
#include <stdio.h>
#include <string.h>
#include <arpa/inet.h>
#include <netinet/in.h>
#include <sys/socket.h>
#include <unistd.h>

static const gne_host_api *api;

static void hasil(gne_ctx *ctx, gne_handle *ret, int code,
		  const char *data, size_t dlen, const char *err)
{
	gne_handle out = api->object(ctx);
	api->obj_set(ctx, out, "code", api->int_new(ctx, code));
	api->obj_set(ctx, out, "data", api->string(ctx, data, dlen));
	api->obj_set(ctx, out, "err", api->string(ctx, err, strlen(err)));
	*ret = out;
}

static int probe(gne_ctx *ctx, int argc, const gne_handle *argv, gne_handle *ret)
{
	char host[256], ca[512], errbuf[256], buf[128];
	int64_t port64 = 0, ins64 = 0, tmo64 = 0;
	size_t n = 0;
	int fd = -1, rc;
	gne_handle tls = 0;
	struct sockaddr_in sa;

	(void)argc;
	host[0] = ca[0] = errbuf[0] = 0;
	if (api->str_copy(ctx, argv[0], host, sizeof(host)) < 0 ||
	    api->get_int(ctx, argv[1], &port64) != 0 ||
	    api->str_copy(ctx, argv[2], ca, sizeof(ca)) < 0 ||
	    api->get_int(ctx, argv[3], &ins64) != 0 ||
	    api->get_int(ctx, argv[4], &tmo64) != 0) {
		api->throw(ctx, "probe_error", 500, "probe(): bad arguments");
		return 1;
	}

	fd = socket(AF_INET, SOCK_STREAM, 0);
	if (fd < 0) {
		hasil(ctx, ret, -3, "", 0, "socket() failed");
		return 0;
	}
	memset(&sa, 0, sizeof sa);
	sa.sin_family = AF_INET;
	sa.sin_port = htons((uint16_t)port64);
	inet_pton(AF_INET, "127.0.0.1", &sa.sin_addr);
	if (connect(fd, (struct sockaddr *)&sa, sizeof sa) != 0) {
		close(fd);
		hasil(ctx, ret, -3, "", 0, "TCP connect failed");
		return 0;
	}

	rc = api->tls_wrap(ctx, (uintptr_t)fd, host,
			   ca[0] ? ca : NULL,
			   (int32_t)(ins64 ? GNE_TLS_INSECURE : 0),
			   (int32_t)tmo64, errbuf, sizeof(errbuf), &tls);
	/* Contract: after wrap our own fd is ours to close for good. */
	close(fd);
	if (rc != 0) {
		hasil(ctx, ret, rc, "", 0, errbuf);
		return 0;
	}
	api->retain(ctx, tls);

	rc = api->tls_write(ctx, tls, "PING\n", 5, (int32_t)tmo64);
	if (rc == 0)
		rc = api->tls_read(ctx, tls, buf, sizeof buf, &n, (int32_t)tmo64);
	if (rc != 0) {
		char msg[64];
		snprintf(msg, sizeof msg, "post-handshake I/O failed (%d)", rc);
		api->tls_close(ctx, tls);
		api->release(ctx, tls);
		hasil(ctx, ret, -4, "", 0, msg);
		return 0;
	}
	api->tls_close(ctx, tls);
	api->release(ctx, tls);
	hasil(ctx, ret, 0, buf, n, "");
	return 0;
}

int gne_module_init(const gne_host_api *a, gne_ctx *ctx, gne_handle *out)
{
	api = a;
	if (api->abi != GNE_ABI) {
		api->throw(ctx, "gne_abi", 500, "ABI mismatch");
		return 1;
	}
	api->define_fn(ctx, "probe", 5, 5, probe);
	*out = api->module(ctx);
	return 0;
}
`

// bangunEkstensiFatal compiles like bangunEkstensi but FAILS the test
// on a compile error (a broken fixture must not read as a skip).
func bangunEkstensiFatal(t *testing.T, dir, name, src string) string {
	t.Helper()
	if _, err := exec.LookPath("gcc"); err != nil {
		t.Skip("gcc not available")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	cPath := filepath.Join(dir, name+".c")
	if err := os.WriteFile(cPath, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, name+".so")
	inc := filepath.Join("..", "..", "..", "include")
	cmd := exec.Command("gcc", "-shared", "-fPIC", "-Wall", "-Wextra",
		"-I", inc, "-o", out, cPath)
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("cannot compile the fixture: %v\n%s", err, b)
	}
	return out
}

// sertifikatTLSUji mints a self-signed server certificate for
// "localhost"/127.0.0.1 and returns its PEM (cert + key).
func sertifikatTLSUji(t *testing.T) (certPEM, keyPEM []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "localhost"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{"localhost"},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	return certPEM, keyPEM
}

// serverPONG answers every TLS connection: read one line, reply
// "PONG\n". Errors (client aborting a failed handshake) are normal.
func serverPONG(t *testing.T, ln net.Listener) {
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				var seen []byte
				tmp := make([]byte, 32)
				for !bytes.Contains(seen, []byte("\n")) {
					n, err := c.Read(tmp)
					if err != nil {
						return
					}
					seen = append(seen, tmp[:n]...)
				}
				c.Write([]byte("PONG\n"))
			}(conn)
		}
	}()
}

// serverBisu accepts TCP but never speaks TLS — used for the timeout
// case. Connections are parked until the test ends.
func serverBisu(t *testing.T, ln net.Listener) {
	var mu sync.Mutex
	var held []net.Conn
	t.Cleanup(func() {
		ln.Close()
		mu.Lock()
		for _, c := range held {
			c.Close()
		}
		mu.Unlock()
	})
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			held = append(held, conn)
			mu.Unlock()
		}
	}()
}

func barisDengan(t *testing.T, out, prefix string) string {
	t.Helper()
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, prefix) {
			return line
		}
	}
	t.Fatalf("no line starting with %q in output:\n%s", prefix, out)
	return ""
}

// TestGNETLS drives the ABI 3 TLS functions end to end.
func TestGNETLS(t *testing.T) {
	dir := t.TempDir()
	bangunEkstensiFatal(t, filepath.Join(dir, "gne"), "tlsfix", fixtureTLS)

	certPEM, keyPEM := sertifikatTLSUji(t)
	pair, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		t.Fatal(err)
	}
	ln, err := tls.Listen("tcp", "127.0.0.1:0",
		&tls.Config{Certificates: []tls.Certificate{pair}, MinVersion: tls.VersionTLS12})
	if err != nil {
		t.Fatal(err)
	}
	serverPONG(t, ln)
	portTLS := ln.Addr().(*net.TCPAddr).Port

	lnBisu, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	serverBisu(t, lnBisu)
	portBisu := lnBisu.Addr().(*net.TCPAddr).Port

	caPath := filepath.Join(dir, "ca.pem")
	if err := os.WriteFile(caPath, certPEM, 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := jalankanMain(t, dir, fmt.Sprintf(`
use "tlsfix"

$ca = tlsfix.probe("localhost", %d, %q, 0, 3000)
print("ca:" + str($ca.code) + "|" + $ca.data + "|err[" + $ca.err + "]")

$noc = tlsfix.probe("localhost", %d, "", 0, 3000)
print("noc:" + str($noc.code) + "|err[" + $noc.err + "]")

$ins = tlsfix.probe("localhost", %d, "", 1, 3000)
print("ins:" + str($ins.code) + "|" + $ins.data)

$bad = tlsfix.probe("wrong.example", %d, %q, 0, 3000)
print("bad:" + str($bad.code) + "|err[" + $bad.err + "]")

$tmo = tlsfix.probe("localhost", %d, "", 1, 300)
print("tmo:" + str($tmo.code) + "|err[" + $tmo.err + "]")
`, portTLS, caPath, portTLS, portTLS, portTLS, caPath, portBisu))
	if err != nil {
		t.Fatalf("eval: %v", err)
	}

	// ca_file: full chain verified, PONG round-tripped, no error.
	if line := barisDengan(t, out, "ca:"); !strings.HasPrefix(line, "ca:0|PONG") {
		t.Errorf("ca_file happy path = %q, want prefix %q", line, "ca:0|PONG")
	}
	if !strings.Contains(out, "|err[]") {
		t.Errorf("happy path must report an empty err, output:\n%s", out)
	}
	// System roots only: self-signed cert must be rejected with a
	// short English reason (not a crash, not empty).
	if line := barisDengan(t, out, "noc:"); !strings.HasPrefix(line, "noc:-1|") || !strings.Contains(line, "x509") {
		t.Errorf("system-roots rejection = %q, want code -1 with x509 reason", line)
	}
	// insecure: verification skipped on purpose.
	if line := barisDengan(t, out, "ins:"); !strings.HasPrefix(line, "ins:0|PONG") {
		t.Errorf("insecure path = %q, want prefix %q", line, "ins:0|PONG")
	}
	// Hostname mismatch: verification must name both certificates.
	if line := barisDengan(t, out, "bad:"); !strings.HasPrefix(line, "bad:-1|") ||
		!strings.Contains(line, "localhost") || !strings.Contains(line, "wrong.example") {
		t.Errorf("hostname mismatch = %q, want code -1 naming localhost/wrong.example", line)
	}
	// Silent server: handshake deadline → GNE_TLS_TIMEOUT (-2).
	if line := barisDengan(t, out, "tmo:"); !strings.HasPrefix(line, "tmo:-2|") {
		t.Errorf("handshake timeout = %q, want prefix %q", line, "tmo:-2|")
	}
}
