//go:build cgo

package interp

// Uji ekstensi resmi ext/smtp: mengompilasi sumber asli (bukan salinan),
// menjalankannya lewat `use "smtp"` pada program Garurda nyata, dengan
// server SMTP tiruan in-process. Suite tetap hermetic — tanpa MTA nyata.

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

// ---- server SMTP tiruan ----

// smtpKonf mengatur perilaku server SMTP tiruan per kasus uji.
type smtpKonf struct {
	greeting  string   // sapaan awal; "" = "220 siap.test ESMTP"
	ehloTolak bool     // balas 502 ke EHLO (uji fallback HELO)
	ekstensi  []string // baris kemampuan EHLO; nil = default (AUTH LOGIN, 8BITMIME)
	authTolak bool     // 535 pada akhir AUTH LOGIN
	rcptTolak string   // substrimat alamat yang dibalas 550 ("" = semua diterima)
	diamEHLO  bool     // berhenti membalas sesudah EHLO (uji timeout)
}

// srvSMTP adalah server SMTP mini in-process: mencatat perintah, kredensial
// AUTH, dan setiap DATA (mentah + setelah unstuffing titik).
type srvSMTP struct {
	konf smtpKonf

	mu       sync.Mutex
	perintah []string
	pesan    []string // DATA setelah unstuffing (headers + badan, CRLF)
	raw      []string // DATA mentah seperti terkirim klien
	authUser string
	authPass string

	done chan struct{}
}

// bukaServerSMTP menjalankan server tiruan pada 127.0.0.1:0 dan
// mengembalikan handle server beserta host/port-nya.
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

// layani menjalankan protokol SMTP pada satu koneksi.
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
				kirim("502 5.5.2 EHLO tidak didukung\r\n")
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
				kirim("535 5.7.8 Autentikasi gagal\r\n")
				continue
			}
			kirim("235 2.7.0 Autentikasi berhasil\r\n")
		case strings.HasPrefix(up, "MAIL FROM:"):
			kirim("250 2.1.0 OK\r\n")
		case strings.HasPrefix(up, "RCPT TO:"):
			if sv.konf.rcptTolak != "" && strings.Contains(line, sv.konf.rcptTolak) {
				kirim("550 5.1.1 Alamat penerima ditolak\r\n")
				continue
			}
			kirim("250 2.1.5 OK\r\n")
		case up == "RSET":
			kirim("250 2.0.0 OK\r\n")
		case up == "DATA":
			if !kirim("354 Akhiri dengan <CR><LF>.<CR><LF>\r\n") {
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
					l = l[1:] // unstuffing ala MTA nyata
				}
				bersih.WriteString(l)
			}
			sv.simpan(raw.String(), bersih.String())
			kirim("250 2.0.0 OK: antre\r\n")
		case up == "QUIT":
			kirim("221 2.0.0 Sampai jumpa\r\n")
			return
		default:
			kirim("500 5.5.2 Perintah tidak dikenal\r\n")
		}
	}
}

// bangunSMTPC mengompilasi ext/smtp/smtp.c (sumber asli) menjadi
// <dir>/gne/smtp.so. Gagal bila gcc tidak ada — ini kode kita, bukan fixture.
func bangunSMTPC(t *testing.T, dir string) {
	t.Helper()
	if _, err := exec.LookPath("gcc"); err != nil {
		t.Skip("gcc tidak tersedia")
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
		t.Fatalf("gagal mengompilasi ext/smtp/smtp.c: %v\n%s", err, b)
	}
}

// ambilHeader mengambil nilai header (setelah "Nama: ") dari blok header.
func ambilHeader(h, nama string) string {
	for _, hl := range strings.Split(h, "\r\n") {
		if strings.HasPrefix(hl, nama) {
			return strings.TrimPrefix(hl, nama)
		}
	}
	return ""
}

// adaPerintah mengecek apakah server menerima perintah dengan awalan tertentu.
func adaPerintah(sv *srvSMTP, prefix string) bool {
	for _, p := range sv.perintahSemua() {
		if strings.HasPrefix(p, prefix) {
			return true
		}
	}
	return false
}

// TestSMTPPenuh menguji alur penuh: AUTH, dua penerima, tiga transaksi DATA
// (teks non-ASCII, kosong, multipart HTML), dot-stuffing, CTE 8bit, close
// idempoten, dan objek basi pasca-close.
func TestSMTPPenuh(t *testing.T) {
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
print($s.subject("Halo dari Garurda"))
print($s.send("Baris pertama\n.dengan titik\nakhir ☕"))
print($s.from("kirim@contoh.id"))
print($s.to("kosong@contoh.id"))
print($s.send(""))
print($s.from("kirim@contoh.id"))
print($s.to("satu@contoh.id"))
print($s.send_html("Versi teks", "<p>Halo <b>dunia</b></p>"))
print($s.close())
print($s.close())
try {
  $s.from("x@y.z")
  print("tidak")
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
		t.Errorf("output =\n%s\nmau =\n%s", got, strings.Join(want, "\n"))
	}

	pesan := sv.pesanSemua()
	if len(pesan) != 3 {
		t.Fatalf("jumlah pesan = %d, mau 3", len(pesan))
	}
	raw := sv.rawSemua()

	// --- pesan 1: text/plain, CTE 8bit, dot-stuffing ---
	h1, b1, ok := strings.Cut(pesan[0], "\r\n\r\n")
	if !ok {
		t.Fatalf("pesan 1 tanpa pemisah header/badan: %q", pesan[0])
	}
	for _, w := range []string{
		"From: kirim@contoh.id\r\n",
		"To: satu@contoh.id, dua@contoh.id\r\n",
		"Subject: Halo dari Garurda\r\n",
		"Content-Type: text/plain; charset=utf-8\r\n",
		"Content-Transfer-Encoding: 8bit\r\n",
		"MIME-Version: 1.0\r\n",
	} {
		if !strings.Contains(h1+"\r\n", w) {
			t.Errorf("header pesan 1 tanpa %q:\n%s", w, h1)
		}
	}
	tgl := ambilHeader(h1, "Date: ")
	ts, err := time.Parse("Mon, 02 Jan 2006 15:04:05 -0700", tgl)
	if err != nil {
		t.Errorf("Date tidak terurai (%v): %q", err, tgl)
	} else {
		if d := time.Since(ts); d < -5*time.Minute || d > 5*time.Minute {
			t.Errorf("Date jauh dari waktu sekarang: %q", tgl)
		}
		if len(tgl) >= 3 && ts.UTC().Format("Mon") != tgl[:3] {
			t.Errorf("nama hari %q tidak cocok dengan tanggalnya (seharusnya %s): %q",
				tgl[:3], ts.UTC().Format("Mon"), tgl)
		}
	}
	if mid := ambilHeader(h1, "Message-ID: "); !strings.HasPrefix(mid, "<") || !strings.HasSuffix(mid, ">") {
		t.Errorf("Message-ID aneh: %q", mid)
	}
	if want := "Baris pertama\r\n.dengan titik\r\nakhir ☕\r\n"; b1 != want {
		t.Errorf("badan 1 = %q, mau %q", b1, want)
	}
	if !strings.Contains(raw[0], "\r\n..dengan titik\r\n") {
		t.Errorf("dot-stuffing hilang di jalur kirim:\n%s", raw[0])
	}

	// --- pesan 2: badan kosong ---
	if _, b2, _ := strings.Cut(pesan[1], "\r\n\r\n"); b2 != "" {
		t.Errorf("badan kosong = %q", b2)
	}
	if !strings.Contains(pesan[1], "To: kosong@contoh.id\r\n") {
		t.Errorf("pesan 2 tanpa penerima:\n%s", pesan[1])
	}

	// --- pesan 3: multipart/alternative ---
	h3, b3, ok := strings.Cut(pesan[2], "\r\n\r\n")
	if !ok {
		t.Fatalf("pesan 3 tanpa pemisah header/badan: %q", pesan[2])
	}
	bnd := ""
	if i := strings.Index(h3, `boundary="`); i >= 0 {
		rest := h3[i+len(`boundary="`):]
		if j := strings.Index(rest, `"`); j >= 0 {
			bnd = rest[:j]
		}
	}
	if !strings.HasPrefix(bnd, "garurda=") {
		t.Fatalf("boundary aneh: %q", bnd)
	}
	if n := strings.Count(b3, "--"+bnd+"\r\n"); n != 2 {
		t.Errorf("baris pembatas multipart = %d, mau 2:\n%s", n, b3)
	}
	if !strings.Contains(b3, "--"+bnd+"--\r\n") {
		t.Errorf("penutup multipart hilang:\n%s", b3)
	}
	for _, w := range []string{
		"Content-Type: text/plain; charset=utf-8\r\n",
		"Content-Type: text/html; charset=utf-8\r\n",
		"Content-Transfer-Encoding: 7bit\r\n",
		"Versi teks",
		"<p>Halo <b>dunia</b></p>",
	} {
		if !strings.Contains(b3, w) {
			t.Errorf("multipart tanpa %q:\n%s", w, b3)
		}
	}

	// --- AUTH & QUIT terekam ---
	u, p := sv.authCreds()
	if u != "pengguna@contoh.id" || p != "rahasia123" {
		t.Errorf("kredensial AUTH = %q/%q, mau pengguna@contoh.id/rahasia123", u, p)
	}
	if !adaPerintah(sv, "QUIT") {
		t.Errorf("server tidak melihat QUIT: %v", sv.perintahSemua())
	}
}

// TestSMTPGalat menguji pemetaan galat (502/504/500), validasi argumen,
// penolakan server, fallback HELO, timeout tengah sesi, dan objek basi.
func TestSMTPGalat(t *testing.T) {
	dir := t.TempDir()
	bangunSMTPC(t, dir)

	// jalankan menjalankan program dengan server tiruan sesuai konf;
	// program memakai dua placeholder %s (host) dan %d (port).
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
  print("tidak")
} catch e {
  print(e.code + "/" + str(e.status))
}
`
	progKonekBuka := `
use "smtp"
try {
  $s = smtp.connect("%s", %d, 1000)
  print("tidak")
} catch e {
  print(e.code + "/" + str(e.status))
}
`

	t.Run("PortMati", func(t *testing.T) {
		out, err := jalankanMain(t, dir, fmt.Sprintf(progKonek, "127.0.0.1", portMati(t)))
		if err != nil {
			t.Fatalf("eval: %v", err)
		}
		if got := strings.TrimSpace(out); got != "smtp_error/502" {
			t.Errorf("port mati = %q, mau smtp_error/502", got)
		}
	})

	t.Run("TutupCepat", func(t *testing.T) {
		host, port := serverTutupCepat(t)
		out, err := jalankanMain(t, dir, fmt.Sprintf(progKonek, host, port))
		if err != nil {
			t.Fatalf("eval: %v", err)
		}
		if got := strings.TrimSpace(out); got != "smtp_error/502" {
			t.Errorf("tutup cepat = %q, mau smtp_error/502", got)
		}
	})

	t.Run("Gantung", func(t *testing.T) {
		host, port := serverGantung(t)
		out, err := jalankanMain(t, dir, fmt.Sprintf(progKonek, host, port))
		if err != nil {
			t.Fatalf("eval: %v", err)
		}
		if got := strings.TrimSpace(out); got != "smtp_error/504" {
			t.Errorf("gantung = %q, mau smtp_error/504", got)
		}
	})

	t.Run("SapaanSalah", func(t *testing.T) {
		_, got := jalankan(t, smtpKonf{greeting: "421 4.7.0 Sedang pemeliharaan"}, progKonekBuka)
		if got != "smtp_error/500" {
			t.Errorf("sapaan 421 = %q, mau smtp_error/500", got)
		}
	})

	t.Run("ArgumenSalah", func(t *testing.T) {
		// Validasi argumen terjadi sebelum dial, jadi tanpa server pun aman.
		out, err := jalankanMain(t, dir, `
use "smtp"
try {
  $s = smtp.connect("127.0.0.1", "bukan-angka", 1000)
  print("tidak")
} catch e {
  print(e.code + "/" + str(e.status))
}
`)
		if err != nil {
			t.Fatalf("eval: %v", err)
		}
		if got := strings.TrimSpace(out); got != "type_error/500" {
			t.Errorf("port string = %q, mau type_error/500", got)
		}
	})

	t.Run("UrutanSalah", func(t *testing.T) {
		sv, got := jalankan(t, smtpKonf{}, `
use "smtp"
$s = smtp.connect("%s", %d, 1000)
try { $s.to("a@b.c") } catch e { print(e.message) }
try { $s.send("x") } catch e { print(e.message) }
try { $s.subject("abc\r\nBcc: rahasia@contoh.id") } catch e { print(e.code) }
print($s.from("kirim@contoh.id"))
print($s.from("pengirim@contoh.id"))
print($s.to("satu@contoh.id"))
print($s.send("pulih dari galat urutan"))
print($s.close())
`)
		want := strings.Join([]string{
			"panggil from() dulu sebelum to()",
			"transaksi belum siap — panggil from() lalu to() minimal sekali sebelum send()",
			"type_error",
			"true",
			"true",
			"true",
			"true",
			"true",
		}, "\n")
		if got != want {
			t.Errorf("output =\n%s\nmau =\n%s", got, want)
		}
		if !adaPerintah(sv, "RSET") {
			t.Errorf("from() kedua tidak memicu RSET: %v", sv.perintahSemua())
		}
		if p := sv.pesanSemua(); len(p) != 1 || !strings.Contains(p[0], "From: pengirim@contoh.id\r\n") {
			t.Errorf("pesan pulih salah: %q", p)
		}
	})

	t.Run("TolakRcpt", func(t *testing.T) {
		sv, got := jalankan(t, smtpKonf{rcptTolak: "tolak@"}, `
use "smtp"
$s = smtp.connect("%s", %d, 1000)
$s.from("kirim@contoh.id")
try { $s.to("tolak@contoh.id") } catch e { print(e.message) }
print($s.to("baik@contoh.id"))
print($s.send("hanya penerima yang diterima"))
print($s.close())
`)
		want := strings.Join([]string{
			"RCPT TO: server membalas 550 5.1.1 Alamat penerima ditolak",
			"true",
			"true",
			"true",
		}, "\n")
		if got != want {
			t.Errorf("output =\n%s\nmau =\n%s", got, want)
		}
		p := sv.pesanSemua()
		if len(p) != 1 || !strings.Contains(p[0], "To: baik@contoh.id\r\n") {
			t.Errorf("daftar penerima pesan salah: %q", p)
		}
	})

	t.Run("AuthTolak", func(t *testing.T) {
		_, got := jalankan(t, smtpKonf{authTolak: true}, `
use "smtp"
$s = smtp.connect("%s", %d, 1000)
try { $s.auth("u", "p") } catch e { print(e.message) }
print($s.close())
`)
		want := strings.Join([]string{
			"AUTH LOGIN (kata sandi): server membalas 535 5.7.8 Autentikasi gagal",
			"true",
		}, "\n")
		if got != want {
			t.Errorf("output =\n%s\nmau =\n%s", got, want)
		}
	})

	t.Run("FallbackHelo", func(t *testing.T) {
		sv, got := jalankan(t, smtpKonf{ehloTolak: true}, `
use "smtp"
$s = smtp.connect("%s", %d, 1000)
try { $s.auth("u", "p") } catch e { print(e.message) }
print($s.close())
`)
		lines := strings.Split(got, "\n")
		if len(lines) != 2 || !strings.Contains(lines[0], "STARTTLS") {
			t.Errorf("auth pasca-HELO = %q, mau pesan soal STARTTLS", got)
		}
		if lines[1] != "true" {
			t.Errorf("close = %q, mau true", lines[1])
		}
		if !adaPerintah(sv, "HELO ") {
			t.Errorf("fallback HELO tidak terkirim: %v", sv.perintahSemua())
		}
	})

	t.Run("DiamSesudahEHLO", func(t *testing.T) {
		_, got := jalankan(t, smtpKonf{diamEHLO: true}, `
use "smtp"
$s = smtp.connect("%s", %d, 600)
try { $s.from("a@b.c") } catch e { print(e.code + "/" + str(e.status)) }
`)
		if got != "smtp_error/504" {
			t.Errorf("diam sesudah EHLO = %q, mau smtp_error/504", got)
		}
	})

	t.Run("ObjekBasi", func(t *testing.T) {
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
			t.Errorf("output =\n%s\nmau =\n%s", got, want)
		}
		if len(sv.perintahSemua()) == 0 {
			t.Error("server tidak menerima apa pun")
		}
	})
}

// TestSMTPTanpa8Bit menguji pemilihan CTE otomatis: tanpa iklan 8BITMIME,
// badan non-ASCII dibungkus base64 dan subjek non-ASCII menjadi encoded-word
// RFC 2047 — server menampung apa adanya, lalu diuraikan kembali di sini.
func TestSMTPTanpa8Bit(t *testing.T) {
	sv, host, port := bukaServerSMTP(t, smtpKonf{ekstensi: []string{"AUTH LOGIN"}})
	dir := t.TempDir()
	bangunSMTPC(t, dir)

	out, err := jalankanMain(t, dir, fmt.Sprintf(`
use "smtp"
$s = smtp.connect("%s", %d, 1000)
$s.from("kirim@contoh.id")
$s.to("satu@contoh.id")
$s.subject("Subjek ☕")
print($s.send("Kopi ☕ panas"))
$s.close()
`, host, port))
	if err != nil {
		t.Fatalf("eval: %v\noutput: %s", err, out)
	}
	if got := strings.TrimSpace(out); got != "true" {
		t.Fatalf("output = %q, mau true", got)
	}
	p := sv.pesanSemua()
	if len(p) != 1 {
		t.Fatalf("jumlah pesan = %d, mau 1", len(p))
	}
	h, b, ok := strings.Cut(p[0], "\r\n\r\n")
	if !ok {
		t.Fatalf("pesan tanpa pemisah header/badan: %q", p[0])
	}
	if !strings.Contains(h+"\r\n", "Content-Transfer-Encoding: base64\r\n") {
		t.Errorf("CTE bukan base64:\n%s", h)
	}

	subj := ambilHeader(h, "Subject: ")
	if !strings.HasPrefix(subj, "=?UTF-8?B?") || !strings.HasSuffix(subj, "?=") {
		t.Fatalf("subjek non-ASCII bukan encoded-word: %q", subj)
	}
	dec, err := base64.StdEncoding.DecodeString(strings.TrimSuffix(strings.TrimPrefix(subj, "=?UTF-8?B?"), "?="))
	if err != nil {
		t.Fatalf("encoded-word tidak terurai: %v", err)
	}
	if string(dec) != "Subjek ☕" {
		t.Errorf("subjek hasil decode = %q, mau %q", string(dec), "Subjek ☕")
	}

	bodyDec, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(b, "\r\n", ""))
	if err != nil {
		t.Fatalf("badan base64 tidak terurai (badan=%q): %v", b, err)
	}
	if string(bodyDec) != "Kopi ☕ panas" {
		t.Errorf("badan hasil decode = %q, mau %q", string(bodyDec), "Kopi ☕ panas")
	}
}
