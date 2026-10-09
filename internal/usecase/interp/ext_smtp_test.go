//go:build cgo

package interp

// Tests the official ext/smtp extension: compiles the original source (not a
// copy), runs it through `use "smtp"` in a real GaLang program, with an
// in-process fake SMTP server. The suite stays hermetic — no real MTA.

import (
	"bufio"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// ---- fake SMTP server ----

// smtpKonf configures the fake SMTP server's behavior per test case.
type smtpKonf struct {
	greeting  string   // initial greeting; "" = "220 siap.test ESMTP"
	ehloTolak bool     // reply 502 to EHLO (tests HELO fallback)
	ekstensi  []string // EHLO capability lines; nil = default (AUTH LOGIN, 8BITMIME)
	authTolak bool     // 535 at the end of AUTH LOGIN
	rcptTolak string   // address substring that gets a 550 reply ("" = accept all)
	diamEHLO  bool     // stop replying after EHLO (tests timeout)
}

// srvSMTP is a mini in-process SMTP server: it records commands, AUTH
// credentials, and every DATA (raw + after dot-unstuffing).
type srvSMTP struct {
	konf smtpKonf

	mu       sync.Mutex
	perintah []string
	pesan    []string // DATA after unstuffing (headers + body, CRLF)
	raw      []string // raw DATA exactly as the client sent it
	authUser string
	authPass string

	done chan struct{}
}

// bukaServerSMTP runs the fake server on 127.0.0.1:0 and
// returns the server handle along with its host/port.
func bukaServerSMTP(t *testing.T, konf smtpKonf) (*srvSMTP, string, int) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	sv := &srvSMTP{konf: konf, done: make(chan struct{})}
	var wg sync.WaitGroup
	t.Cleanup(func() {
		close(sv.done)
		ln.Close()
		wg.Wait()
	})
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			wg.Add(1)
			go func(c net.Conn) {
				defer wg.Done()
				sv.layani(c)
			}(c)
		}
	}()
	return sv, "127.0.0.1", portDari(ln)
}

func (sv *srvSMTP) catat(p string) {
	sv.mu.Lock()
	sv.perintah = append(sv.perintah, p)
	sv.mu.Unlock()
}

func (sv *srvSMTP) simpan(raw, bersih string) {
	sv.mu.Lock()
	sv.raw = append(sv.raw, raw)
	sv.pesan = append(sv.pesan, bersih)
	sv.mu.Unlock()
}

func (sv *srvSMTP) setAuth(u, p string) {
	sv.mu.Lock()
	if u != "" {
		sv.authUser = u
	}
	if p != "" {
		sv.authPass = p
	}
	sv.mu.Unlock()
}

func (sv *srvSMTP) perintahSemua() []string {
	sv.mu.Lock()
	defer sv.mu.Unlock()
	return append([]string(nil), sv.perintah...)
}

func (sv *srvSMTP) pesanSemua() []string {
	sv.mu.Lock()
	defer sv.mu.Unlock()
	return append([]string(nil), sv.pesan...)
}

func (sv *srvSMTP) rawSemua() []string {
	sv.mu.Lock()
	defer sv.mu.Unlock()
	return append([]string(nil), sv.raw...)
}

func (sv *srvSMTP) authCreds() (string, string) {
	sv.mu.Lock()
	defer sv.mu.Unlock()
	return sv.authUser, sv.authPass
}

// layani runs the SMTP protocol on one connection.
func (sv *srvSMTP) layani(c net.Conn) {
	defer c.Close()
	br := bufio.NewReader(c)
	kirim := func(s string) bool {
		_, err := io.WriteString(c, s)
		return err == nil
	}
	g := sv.konf.greeting
	if g == "" {
		g = "220 siap.test ESMTP"
	}
	if !kirim(g + "\r\n") {
		return
	}
	diam := false
	for {
		if diam {
			select {
			case <-sv.done:
			case <-time.After(30 * time.Second):
			}
			return
		}
		line, err := br.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		sv.catat(line)
		up := strings.ToUpper(line)
		switch {
		case strings.HasPrefix(up, "EHLO "):
			if sv.konf.ehloTolak {
				kirim("502 5.5.2 EHLO not supported\r\n")
				continue
			}
			eks := sv.konf.ekstensi
			if eks == nil {
				eks = []string{"AUTH LOGIN", "8BITMIME"}
			}
			if len(eks) == 0 {
				kirim("250 siap.test\r\n")
			} else {
				var b strings.Builder
				for i, e := range eks {
					if i == len(eks)-1 {
						fmt.Fprintf(&b, "250 %s\r\n", e)
					} else {
						fmt.Fprintf(&b, "250-%s\r\n", e)
					}
				}
				kirim(b.String())
			}
			if sv.konf.diamEHLO {
				diam = true
			}
		case strings.HasPrefix(up, "HELO "):
			kirim("250 siap.test\r\n")
		case up == "AUTH LOGIN":
			if !kirim("334 VXNlcm5hbWU6\r\n") {
				return
			}
			u, err := br.ReadString('\n')
			if err != nil {
				return
			}
			sv.catat(strings.TrimSpace(u))
			if d, err := base64.StdEncoding.DecodeString(strings.TrimSpace(u)); err == nil {
				sv.setAuth(string(d), "")
			}
			if !kirim("334 UGFzc3dvcmQ6\r\n") {
				return
			}
			p, err := br.ReadString('\n')
			if err != nil {
				return
			}
			sv.catat(strings.TrimSpace(p))
			if d, err := base64.StdEncoding.DecodeString(strings.TrimSpace(p)); err == nil {
				sv.setAuth("", string(d))
			}
			if sv.konf.authTolak {
				kirim("535 5.7.8 Authentication failed\r\n")
				continue
			}
			kirim("235 2.7.0 Authentication successful\r\n")
		case strings.HasPrefix(up, "MAIL FROM:"):
			kirim("250 2.1.0 OK\r\n")
		case strings.HasPrefix(up, "RCPT TO:"):
			if sv.konf.rcptTolak != "" && strings.Contains(line, sv.konf.rcptTolak) {
				kirim("550 5.1.1 Recipient address rejected\r\n")
				continue
			}
			kirim("250 2.1.5 OK\r\n")
		case up == "RSET":
			kirim("250 2.0.0 OK\r\n")
		case up == "DATA":
			if !kirim("354 End with <CR><LF>.<CR><LF>\r\n") {
				return
			}
			var raw, bersih strings.Builder
			for {
				l, err := br.ReadString('\n')
				if err != nil {
					return
				}
				if l == ".\r\n" {
					break
				}
				raw.WriteString(l)
				if strings.HasPrefix(l, "..") {
					l = l[1:] // unstuffing like a real MTA
				}
				bersih.WriteString(l)
			}
			sv.simpan(raw.String(), bersih.String())
			kirim("250 2.0.0 OK: queued\r\n")
		case up == "QUIT":
			kirim("221 2.0.0 Goodbye\r\n")
			return
		default:
			kirim("500 5.5.2 Command not recognized\r\n")
		}
	}
}

// bangunSMTPC compiles ext/smtp/smtp.c (the original source) into
// <dir>/gne/smtp.so. Fails if gcc is missing — this is our code, not a fixture.
func bangunSMTPC(t *testing.T, dir string) {
	t.Helper()
	if _, err := exec.LookPath("gcc"); err != nil {
		t.Skip("gcc not available")
	}
	src := filepath.Join("..", "..", "..", "ext", "smtp", "smtp.c")
	inc := filepath.Join("..", "..", "..", "include")
	out := filepath.Join(dir, "gne", "smtp.so")
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("gcc", "-shared", "-fPIC", "-Wall", "-Wextra",
		"-I", inc, "-o", out, src)
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("failed to compile ext/smtp/smtp.c: %v\n%s", err, b)
	}
}

// ambilHeader extracts a header value (after "Name: ") from a header block.
func ambilHeader(h, nama string) string {
	for _, hl := range strings.Split(h, "\r\n") {
		if strings.HasPrefix(hl, nama) {
			return strings.TrimPrefix(hl, nama)
		}
	}
	return ""
}

// adaPerintah checks whether the server received a command with a given prefix.
func adaPerintah(sv *srvSMTP, prefix string) bool {
	for _, p := range sv.perintahSemua() {
		if strings.HasPrefix(p, prefix) {
			return true
		}
	}
	return false
}

// TestSMTPFull exercises the full flow: AUTH, two recipients, three DATA
// transactions (non-ASCII text, empty, multipart HTML), dot-stuffing, 8bit
// CTE, idempotent close, and a stale object after close.
func TestSMTPFull(t *testing.T) {
	sv, host, port := bukaServerSMTP(t, smtpKonf{})
	dir := t.TempDir()
	bangunSMTPC(t, dir)

	out, err := jalankanMain(t, dir, fmt.Sprintf(`
use "smtp"

$s = smtp.connect("%s", %d, 3000)
print($s.auth("pengguna@contoh.id", "rahasia123"))
print($s.from("kirim@contoh.id"))
print($s.to("satu@contoh.id"))
print($s.to("dua@contoh.id"))
print($s.subject("Hello from GaLang"))
print($s.send("First line\n.with dot\nlast ☕"))
print($s.from("kirim@contoh.id"))
print($s.to("kosong@contoh.id"))
print($s.send(""))
print($s.from("kirim@contoh.id"))
print($s.to("satu@contoh.id"))
print($s.send_html("Text version", "<p>Hello <b>world</b></p>"))
print($s.close())
print($s.close())
try {
  $s.from("x@y.z")
  print("no")
} catch e {
  print(e.code + "/" + str(e.status))
}
`, host, port))
	if err != nil {
		t.Fatalf("eval: %v\noutput: %s", err, out)
	}
	var want []string
	for i := 0; i < 13; i++ {
		want = append(want, "true")
	}
	want = append(want, "false", "smtp_error/500")
	if got := strings.TrimSpace(out); got != strings.Join(want, "\n") {
		t.Errorf("output =\n%s\nwant =\n%s", got, strings.Join(want, "\n"))
	}

	pesan := sv.pesanSemua()
	if len(pesan) != 3 {
		t.Fatalf("message count = %d, want 3", len(pesan))
	}
	raw := sv.rawSemua()

	// --- message 1: text/plain, 8bit CTE, dot-stuffing ---
	h1, b1, ok := strings.Cut(pesan[0], "\r\n\r\n")
	if !ok {
		t.Fatalf("message 1 has no header/body separator: %q", pesan[0])
	}
	for _, w := range []string{
		"From: kirim@contoh.id\r\n",
		"To: satu@contoh.id, dua@contoh.id\r\n",
		"Subject: Hello from GaLang\r\n",
		"Content-Type: text/plain; charset=utf-8\r\n",
		"Content-Transfer-Encoding: 8bit\r\n",
		"MIME-Version: 1.0\r\n",
	} {
		if !strings.Contains(h1+"\r\n", w) {
			t.Errorf("message 1 header missing %q:\n%s", w, h1)
		}
	}
	tgl := ambilHeader(h1, "Date: ")
	ts, err := time.Parse("Mon, 02 Jan 2006 15:04:05 -0700", tgl)
	if err != nil {
		t.Errorf("Date failed to parse (%v): %q", err, tgl)
	} else {
		if d := time.Since(ts); d < -5*time.Minute || d > 5*time.Minute {
			t.Errorf("Date far from the current time: %q", tgl)
		}
		if len(tgl) >= 3 && ts.UTC().Format("Mon") != tgl[:3] {
			t.Errorf("weekday name %q does not match the date (expected %s): %q",
				tgl[:3], ts.UTC().Format("Mon"), tgl)
		}
	}
	if mid := ambilHeader(h1, "Message-ID: "); !strings.HasPrefix(mid, "<") || !strings.HasSuffix(mid, ">") {
		t.Errorf("weird Message-ID: %q", mid)
	}
	if want := "First line\r\n.with dot\r\nlast ☕\r\n"; b1 != want {
		t.Errorf("body 1 = %q, want %q", b1, want)
	}
	if !strings.Contains(raw[0], "\r\n..with dot\r\n") {
		t.Errorf("dot-stuffing missing on the wire:\n%s", raw[0])
	}

	// --- message 2: empty body ---
	if _, b2, _ := strings.Cut(pesan[1], "\r\n\r\n"); b2 != "" {
		t.Errorf("empty body = %q", b2)
	}
	if !strings.Contains(pesan[1], "To: kosong@contoh.id\r\n") {
		t.Errorf("message 2 has no recipient:\n%s", pesan[1])
	}

	// --- message 3: multipart/alternative ---
	h3, b3, ok := strings.Cut(pesan[2], "\r\n\r\n")
	if !ok {
		t.Fatalf("message 3 has no header/body separator: %q", pesan[2])
	}
	bnd := ""
	if i := strings.Index(h3, `boundary="`); i >= 0 {
		rest := h3[i+len(`boundary="`):]
		if j := strings.Index(rest, `"`); j >= 0 {
			bnd = rest[:j]
		}
	}
	if !strings.HasPrefix(bnd, "galang=") {
		t.Fatalf("weird boundary: %q", bnd)
	}
	if n := strings.Count(b3, "--"+bnd+"\r\n"); n != 2 {
		t.Errorf("multipart delimiter lines = %d, want 2:\n%s", n, b3)
	}
	if !strings.Contains(b3, "--"+bnd+"--\r\n") {
		t.Errorf("multipart terminator missing:\n%s", b3)
	}
	for _, w := range []string{
		"Content-Type: text/plain; charset=utf-8\r\n",
		"Content-Type: text/html; charset=utf-8\r\n",
		"Content-Transfer-Encoding: 7bit\r\n",
		"Text version",
		"<p>Hello <b>world</b></p>",
	} {
		if !strings.Contains(b3, w) {
			t.Errorf("multipart missing %q:\n%s", w, b3)
		}
	}

	// --- AUTH & QUIT recorded ---
	u, p := sv.authCreds()
	if u != "pengguna@contoh.id" || p != "rahasia123" {
		t.Errorf("AUTH credentials = %q/%q, want pengguna@contoh.id/rahasia123", u, p)
	}
	if !adaPerintah(sv, "QUIT") {
		t.Errorf("server never saw QUIT: %v", sv.perintahSemua())
	}
}

// TestSMTPErrors tests error mapping (502/504/500), argument validation,
// server rejections, HELO fallback, mid-session timeouts, and stale objects.
func TestSMTPErrors(t *testing.T) {
	dir := t.TempDir()
	bangunSMTPC(t, dir)

	// jalankan runs the program against a fake server per konf;
	// the program uses two placeholders %s (host) and %d (port).
	jalankan := func(t *testing.T, konf smtpKonf, src string) (*srvSMTP, string) {
		t.Helper()
		sv, host, port := bukaServerSMTP(t, konf)
		out, err := jalankanMain(t, dir, fmt.Sprintf(src, host, port))
		if err != nil {
			t.Fatalf("eval: %v\noutput: %s", err, out)
		}
		return sv, strings.TrimSpace(out)
	}

	progKonek := `
use "smtp"
try {
  $s = smtp.connect("%s", %d, 500)
  $s.close()
  print("no")
} catch e {
  print(e.code + "/" + str(e.status))
}
`
	progKonekBuka := `
use "smtp"
try {
  $s = smtp.connect("%s", %d, 1000)
  print("no")
} catch e {
  print(e.code + "/" + str(e.status))
}
`

	t.Run("DeadPort", func(t *testing.T) {
		out, err := jalankanMain(t, dir, fmt.Sprintf(progKonek, "127.0.0.1", portMati(t)))
		if err != nil {
			t.Fatalf("eval: %v", err)
		}
		if got := strings.TrimSpace(out); got != "smtp_error/502" {
			t.Errorf("dead port = %q, want smtp_error/502", got)
		}
	})

	t.Run("QuickClose", func(t *testing.T) {
		host, port := serverTutupCepat(t)
		out, err := jalankanMain(t, dir, fmt.Sprintf(progKonek, host, port))
		if err != nil {
			t.Fatalf("eval: %v", err)
		}
		if got := strings.TrimSpace(out); got != "smtp_error/502" {
			t.Errorf("quick close = %q, want smtp_error/502", got)
		}
	})

	t.Run("Hang", func(t *testing.T) {
		host, port := serverGantung(t)
		out, err := jalankanMain(t, dir, fmt.Sprintf(progKonek, host, port))
		if err != nil {
			t.Fatalf("eval: %v", err)
		}
		if got := strings.TrimSpace(out); got != "smtp_error/504" {
			t.Errorf("hang = %q, want smtp_error/504", got)
		}
	})

	t.Run("BadGreeting", func(t *testing.T) {
		_, got := jalankan(t, smtpKonf{greeting: "421 4.7.0 Under maintenance"}, progKonekBuka)
		if got != "smtp_error/500" {
			t.Errorf("greeting 421 = %q, want smtp_error/500", got)
		}
	})

	t.Run("BadArgument", func(t *testing.T) {
		// Argument validation happens before dialing, so it is safe without a server.
		out, err := jalankanMain(t, dir, `
use "smtp"
try {
  $s = smtp.connect("127.0.0.1", "not-a-number", 1000)
  print("no")
} catch e {
  print(e.code + "/" + str(e.status))
}
`)
		if err != nil {
			t.Fatalf("eval: %v", err)
		}
		if got := strings.TrimSpace(out); got != "type_error/500" {
			t.Errorf("string port = %q, want type_error/500", got)
		}
	})

	t.Run("WrongOrder", func(t *testing.T) {
		sv, got := jalankan(t, smtpKonf{}, `
use "smtp"
$s = smtp.connect("%s", %d, 1000)
try { $s.to("a@b.c") } catch e { print(e.message) }
try { $s.send("x") } catch e { print(e.message) }
try { $s.subject("abc\r\nBcc: rahasia@contoh.id") } catch e { print(e.code) }
print($s.from("kirim@contoh.id"))
print($s.from("pengirim@contoh.id"))
print($s.to("satu@contoh.id"))
print($s.send("recover from ordering error"))
print($s.close())
`)
		want := strings.Join([]string{
			"call from() before to()",
			"transaction not ready — call from() then to() at least once before send()",
			"type_error",
			"true",
			"true",
			"true",
			"true",
			"true",
		}, "\n")
		if got != want {
			t.Errorf("output =\n%s\nwant =\n%s", got, want)
		}
		if !adaPerintah(sv, "RSET") {
			t.Errorf("second from() did not trigger RSET: %v", sv.perintahSemua())
		}
		if p := sv.pesanSemua(); len(p) != 1 || !strings.Contains(p[0], "From: pengirim@contoh.id\r\n") {
			t.Errorf("wrong recovery message: %q", p)
		}
	})

	t.Run("RejectRcpt", func(t *testing.T) {
		sv, got := jalankan(t, smtpKonf{rcptTolak: "tolak@"}, `
use "smtp"
$s = smtp.connect("%s", %d, 1000)
$s.from("kirim@contoh.id")
try { $s.to("tolak@contoh.id") } catch e { print(e.message) }
print($s.to("baik@contoh.id"))
print($s.send("only accepted recipients"))
print($s.close())
`)
		want := strings.Join([]string{
			"RCPT TO: server replied 550 5.1.1 Recipient address rejected",
			"true",
			"true",
			"true",
		}, "\n")
		if got != want {
			t.Errorf("output =\n%s\nwant =\n%s", got, want)
		}
		p := sv.pesanSemua()
		if len(p) != 1 || !strings.Contains(p[0], "To: baik@contoh.id\r\n") {
			t.Errorf("wrong message recipient list: %q", p)
		}
	})

	t.Run("AuthReject", func(t *testing.T) {
		_, got := jalankan(t, smtpKonf{authTolak: true}, `
use "smtp"
$s = smtp.connect("%s", %d, 1000)
try { $s.auth("u", "p") } catch e { print(e.message) }
print($s.close())
`)
		want := strings.Join([]string{
			"AUTH LOGIN (password): server replied 535 5.7.8 Authentication failed",
			"true",
		}, "\n")
		if got != want {
			t.Errorf("output =\n%s\nwant =\n%s", got, want)
		}
	})

	t.Run("HeloFallback", func(t *testing.T) {
		sv, got := jalankan(t, smtpKonf{ehloTolak: true}, `
use "smtp"
$s = smtp.connect("%s", %d, 1000)
try { $s.auth("u", "p") } catch e { print(e.message) }
print($s.close())
`)
		lines := strings.Split(got, "\n")
		if len(lines) != 2 || !strings.Contains(lines[0], "STARTTLS") {
			t.Errorf("post-HELO auth = %q, want a message about STARTTLS", got)
		}
		if lines[1] != "true" {
			t.Errorf("close = %q, want true", lines[1])
		}
		if !adaPerintah(sv, "HELO ") {
			t.Errorf("HELO fallback was not sent: %v", sv.perintahSemua())
		}
	})

	t.Run("SilentAfterEHLO", func(t *testing.T) {
		_, got := jalankan(t, smtpKonf{diamEHLO: true}, `
use "smtp"
$s = smtp.connect("%s", %d, 600)
try { $s.from("a@b.c") } catch e { print(e.code + "/" + str(e.status)) }
`)
		if got != "smtp_error/504" {
			t.Errorf("silent after EHLO = %q, want smtp_error/504", got)
		}
	})

	t.Run("StaleObject", func(t *testing.T) {
		sv, host, port := bukaServerSMTP(t, smtpKonf{})
		out, err := jalankanMain(t, dir, fmt.Sprintf(`
use "smtp"
$s1 = smtp.connect("%s", %d, 1000)
print($s1.close())
$s2 = smtp.connect("%s", %d, 1000)
try { $s1.from("a@b.c") } catch e { print(e.code + "/" + str(e.status)) }
print($s2.close())
`, host, port, host, port))
		if err != nil {
			t.Fatalf("eval: %v\noutput: %s", err, out)
		}
		want := strings.Join([]string{"true", "smtp_error/500", "true"}, "\n")
		if got := strings.TrimSpace(out); got != want {
			t.Errorf("output =\n%s\nwant =\n%s", got, want)
		}
		if len(sv.perintahSemua()) == 0 {
			t.Error("server received nothing")
		}
	})
}

// TestSMTPWithout8Bit tests automatic CTE selection: without 8BITMIME
// advertised, a non-ASCII body is base64-wrapped and a non-ASCII subject
// becomes an RFC 2047 encoded-word — accepted as-is, decoded back here.
func TestSMTPWithout8Bit(t *testing.T) {
	sv, host, port := bukaServerSMTP(t, smtpKonf{ekstensi: []string{"AUTH LOGIN"}})
	dir := t.TempDir()
	bangunSMTPC(t, dir)

	out, err := jalankanMain(t, dir, fmt.Sprintf(`
use "smtp"
$s = smtp.connect("%s", %d, 1000)
$s.from("kirim@contoh.id")
$s.to("satu@contoh.id")
$s.subject("Subject ☕")
print($s.send("Hot ☕ coffee"))
$s.close()
`, host, port))
	if err != nil {
		t.Fatalf("eval: %v\noutput: %s", err, out)
	}
	if got := strings.TrimSpace(out); got != "true" {
		t.Fatalf("output = %q, want true", got)
	}
	p := sv.pesanSemua()
	if len(p) != 1 {
		t.Fatalf("message count = %d, want 1", len(p))
	}
	h, b, ok := strings.Cut(p[0], "\r\n\r\n")
	if !ok {
		t.Fatalf("message has no header/body separator: %q", p[0])
	}
	if !strings.Contains(h+"\r\n", "Content-Transfer-Encoding: base64\r\n") {
		t.Errorf("CTE is not base64:\n%s", h)
	}

	subj := ambilHeader(h, "Subject: ")
	if !strings.HasPrefix(subj, "=?UTF-8?B?") || !strings.HasSuffix(subj, "?=") {
		t.Fatalf("non-ASCII subject is not an encoded-word: %q", subj)
	}
	dec, err := base64.StdEncoding.DecodeString(strings.TrimSuffix(strings.TrimPrefix(subj, "=?UTF-8?B?"), "?="))
	if err != nil {
		t.Fatalf("encoded-word failed to decode: %v", err)
	}
	if string(dec) != "Subject ☕" {
		t.Errorf("decoded subject = %q, want %q", string(dec), "Subject ☕")
	}

	bodyDec, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(b, "\r\n", ""))
	if err != nil {
		t.Fatalf("base64 body failed to decode (body=%q): %v", b, err)
	}
	if string(bodyDec) != "Hot ☕ coffee" {
		t.Errorf("decoded body = %q, want %q", string(bodyDec), "Hot ☕ coffee")
	}
}
