//go:build cgo

package interp

// Uji ekstensi resmi ext/jwt: mengompilasi sumber asli lalu memverifikasi
// tanda tangan HS256/HS384/HS512 secara SILANG terhadap crypto/hmac +
// crypto/sha* bawaan Go (bukan sekadar "tidak error"), plus jalur galat:
// rahasia salah, payload dirusak, alg di luar allowlist (anti
// alg-confusion), klaim exp/nbf, dan decode tanpa verifikasi.

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"fmt"
	"hash"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// bangunJwtC mengompilasi ext/jwt/jwt.c (sumber asli) menjadi
// <dir>/gne/jwt.so. Gagal bila gcc tidak ada — ini kode kita, bukan fixture.
func bangunJwtC(t *testing.T, dir string) {
	t.Helper()
	if _, err := exec.LookPath("gcc"); err != nil {
		t.Skip("gcc tidak tersedia")
	}
	src := filepath.Join("..", "..", "..", "ext", "jwt", "jwt.c")
	inc := filepath.Join("..", "..", "..", "include")
	out := filepath.Join(dir, "gne", "jwt.so")
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("gcc", "-shared", "-fPIC", "-Wall", "-Wextra",
		"-I", inc, "-o", out, src)
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("gagal mengompilasi ext/jwt/jwt.c: %v\n%s", err, b)
	}
}

// hashHs mengembalikan fungsi hash sesuai algoritma (panic bila di luar
// tiga algoritma HMAC-S2 yang didukung).
func hashHs(alg string) func() hash.Hash {
	switch alg {
	case "HS256":
		return sha256.New
	case "HS384":
		return sha512.New384
	case "HS512":
		return sha512.New
	}
	panic("algoritma tak dikenal: " + alg)
}

// cekToken memverifikasi token JWT buatan ekstensi terhadap implementasi
// standar Go: tanda tangan HMAC-nya harus identik (bukti rantai
// SHA-2 + HMAC benar), dan payload-nya harus klaim yang ditandatangani.
func cekToken(t *testing.T, token, alg, secret, klaim string) {
	t.Helper()
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		t.Fatalf("token %q bukan tiga segmen", token)
	}
	mac := hmac.New(hashHs(alg), []byte(secret))
	mac.Write([]byte(parts[0] + "." + parts[1]))
	want := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	if parts[2] != want {
		t.Errorf("tanda tangan %s tidak cocok stdlib:\next: %s\nstd: %s",
			alg, parts[2], want)
	}
	hdr, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		t.Fatalf("header tidak valid base64url: %v", err)
	}
	if !strings.Contains(string(hdr), `"alg":"`+alg+`"`) {
		t.Errorf("header %q tidak memuat alg %s", hdr, alg)
	}
	badan, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatalf("payload tidak valid base64url: %v", err)
	}
	if string(badan) != klaim {
		t.Errorf("payload = %q, mau %q", badan, klaim)
	}
}

// rusakToken mengubah satu karakter pertama segmen payload sehingga
// tanda tangan sah menjadi tidak cocok (payload berbeda, MAC lama).
func rusakToken(t *testing.T, token string) string {
	t.Helper()
	parts := strings.Split(token, ".")
	if len(parts) != 3 || parts[1] == "" {
		t.Fatalf("token %q tidak bisa dirusak", token)
	}
	if parts[1][0] == 'A' {
		parts[1] = "B" + parts[1][1:]
	} else {
		parts[1] = "A" + parts[1][1:]
	}
	return parts[0] + "." + parts[1] + "." + parts[2]
}

// rusakTapiJsonValid mengubah satu karakter di dalam klaim sehingga
// payload tetap JSON sah tetapi tanda tangan tidak cocok — memisahkan
// jalur "signature tidak valid" (401) dari "payload bukan JSON" (400).
func rusakTapiJsonValid(t *testing.T, token string) string {
	t.Helper()
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		t.Fatalf("token %q bukan tiga segmen", token)
	}
	b, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatalf("payload tidak valid: %v", err)
	}
	baru := bytes.Replace(b, []byte("budi"), []byte("budx"), 1)
	if string(baru) == string(b) {
		t.Fatalf("klaim %q tidak memuat 'budi' untuk dirusak", b)
	}
	return parts[0] + "." + base64.RawURLEncoding.EncodeToString(baru) + "." + parts[2]
}

// tokenManual menyusun token mentah dari header+klaim+rahasia memakai
// stdlib Go (dipakai untuk token "alg:none"/RS256 di luar allowlist dan
// token kedaluwarsa untuk decode tanpa verifikasi).
func tokenManual(header, klaim, secret string) string {
	enc := base64.RawURLEncoding.EncodeToString
	menandatangani := enc([]byte(header)) + "." + enc([]byte(klaim))
	if secret == "" {
		return menandatangani + "."
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(menandatangani))
	return menandatangani + "." + enc(mac.Sum(nil))
}

// TestJwtPutarLewatGo menandatangani dengan ekstensi di ketiga algoritma,
// lalu memverifikasi tokennya dengan crypto/hmac bawaan Go — rantai
// SHA-2/HMAC ekstensi terbukti identik dengan implementasi tepercaya.
func TestJwtPutarLewatGo(t *testing.T) {
	dir := t.TempDir()
	bangunJwtC(t, dir)

	klaim := `{"sub":"budi","role":"admin"}`
	out, err := jalankanMain(t, dir, fmt.Sprintf(`
use "jwt"
$t256 = jwt.sign(%q, "rahasia", "HS256")
print($t256)
print(jwt.verify($t256, "rahasia"))
$t384 = jwt.sign(%q, "rahasia", "HS384")
print($t384)
print(jwt.verify($t384, "rahasia"))
$t512 = jwt.sign(%q, "rahasia", "HS512")
print($t512)
print(jwt.verify($t512, "rahasia"))
$tdef = jwt.sign(%q, "rahasia")
print($tdef)
print(jwt.decode($t256))
print(jwt.decode($tdef))
`, klaim, klaim, klaim, klaim))
	if err != nil {
		t.Fatalf("eval: %v", err)
	}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 9 {
		t.Fatalf("jumlah baris = %d, mau 9:\n%s", len(lines), out)
	}
	// Token buatan ekstensi diverifikasi ulang oleh stdlib Go.
	cekToken(t, lines[0], "HS256", "rahasia", klaim)
	cekToken(t, lines[2], "HS384", "rahasia", klaim)
	cekToken(t, lines[4], "HS512", "rahasia", klaim)
	cekToken(t, lines[6], "HS256", "rahasia", klaim) // default = HS256
	// verify dan decode mengembalikan klaim apa adanya.
	for _, i := range []int{1, 3, 5, 7, 8} {
		if lines[i] != klaim {
			t.Errorf("baris %d = %q, mau klaim %q", i+1, lines[i], klaim)
		}
	}
}

// TestJwtGalat menguji seluruh jalur galat: rahasia salah, payload
// dirusak, alg di luar allowlist, klaim exp/nbf, dan argumen salah.
func TestJwtGalat(t *testing.T) {
	dir := t.TempDir()
	bangunJwtC(t, dir)

	sah := tokenManual(`{"alg":"HS256","typ":"JWT"}`, `{"sub":"budi"}`, "rahasia")
	rusakJson := rusakToken(t, sah)
	rusakValid := rusakTapiJsonValid(t, sah)
	algNoneKosong := tokenManual(`{"alg":"none"}`, `{"sub":"budi"}`, "")
	algNoneSignature := tokenManual(`{"alg":"none"}`, `{"sub":"budi"}`, "x")
	algRs := tokenManual(`{"alg":"RS256"}`, `{"sub":"budi"}`, "x")

	out, err := jalankanMain(t, dir, fmt.Sprintf(`
use "jwt"
$t = jwt.sign(%q, "rahasia", "HS256")
try {
  jwt.verify($t, "salah")
  print("tidak")
} catch e {
  print(e.code + "/" + str(e.status))
}
try {
  jwt.verify(%q, "rahasia")
  print("tidak")
} catch e {
  print(e.code + "/" + str(e.status))
}
try {
  jwt.verify(%q, "rahasia")
  print("tidak")
} catch e {
  print(e.code + "/" + str(e.status))
}
try {
  jwt.verify(%q, "rahasia")
  print("tidak")
} catch e {
  print(e.code + "/" + str(e.status))
}
try {
  jwt.verify(%q, "rahasia")
  print("tidak")
} catch e {
  print(e.code + "/" + str(e.status))
}
try {
  jwt.verify(%q, "rahasia")
  print("tidak")
} catch e {
  print(e.code + "/" + str(e.status))
}
try {
  jwt.verify("tidakvalid", "rahasia")
  print("tidak")
} catch e {
  print(e.code + "/" + str(e.status))
}
try {
  jwt.decode("abc.d ef")
  print("tidak")
} catch e {
  print(e.code + "/" + str(e.status))
}
$e1 = jwt.sign(%q, "rahasia")
try {
  jwt.verify($e1, "rahasia")
  print("tidak")
} catch e {
  print(e.code + "/" + str(e.status))
}
$n1 = jwt.sign(%q, "rahasia")
try {
  jwt.verify($n1, "rahasia")
  print("tidak")
} catch e {
  print(e.code + "/" + str(e.status))
}
$es = jwt.sign(%q, "rahasia")
try {
  jwt.verify($es, "rahasia")
  print("tidak")
} catch e {
  print(e.code + "/" + str(e.status))
}
try {
  jwt.sign(123, "rahasia")
  print("tidak")
} catch e {
  print(e.code)
}
$ok = jwt.sign(%q, "rahasia")
print(jwt.verify($ok, "rahasia"))
`, `{"sub":"budi"}`, rusakJson, rusakValid, algNoneKosong, algNoneSignature,
		algRs, `{"exp":1000000000}`, `{"nbf":4102444800}`, `{"exp":"nanti"}`,
		`{"exp":4102444800}`))
	if err != nil {
		t.Fatalf("eval: %v", err)
	}

	want := strings.Join([]string{
		"jwt_error/401",      // rahasia salah
		"jwt_error/400",      // payload bukan JSON objek (dinilai sebelum MAC)
		"jwt_error/401",      // payload tetap JSON tapi MAC tidak cocok
		"jwt_error/400",      // alg:none tanpa signature → format ditolak
		"jwt_error/401",      // alg:none dengan signature → di luar allowlist
		"jwt_error/401",      // RS256 di luar allowlist
		"jwt_error/400",      // token satu segmen
		"jwt_error/400",      // decode segmen bukan base64url
		"jwt_error/401",      // exp lampau
		"jwt_error/401",      // nbf masa depan
		"jwt_error/400",      // exp bukan angka
		"type_error",         // claims non-string
		`{"exp":4102444800}`, // exp jauh → verify sukses
	}, "\n")
	if got := strings.TrimRight(out, "\n"); got != want {
		t.Errorf("keluaran salah:\ngot:\n%s\nwant:\n%s", got, want)
	}
}

// TestJwtDecodeTanpaVerifikasi memastikan decode mengembalikan klaim
// apa adanya tanpa cek tanda tangan maupun masa berlaku — untuk
// membaca token yang belum/diverifikasi terpisah.
func TestJwtDecodeTanpaVerifikasi(t *testing.T) {
	dir := t.TempDir()
	bangunJwtC(t, dir)

	kedaluwarsa := tokenManual(`{"alg":"HS256","typ":"JWT"}`,
		`{"exp":1000000000}`, "rahasia")
	// Signature palsu pun tidak menghalangi decode — decode memang
	// tanpa verifikasi (bacalah hanya token yang diverifikasi terpisah).
	algNone := tokenManual(`{"alg":"none"}`, `{"sub":"budi"}`, "x")

	out, err := jalankanMain(t, dir, fmt.Sprintf(`
use "jwt"
print(jwt.decode(%q))
print(jwt.decode(%q))
`, kedaluwarsa, algNone))
	if err != nil {
		t.Fatalf("eval: %v", err)
	}
	want := strings.Join([]string{
		`{"exp":1000000000}`,
		`{"sub":"budi"}`,
	}, "\n")
	if got := strings.TrimRight(out, "\n"); got != want {
		t.Errorf("keluaran decode salah:\ngot:\n%s\nwant:\n%s", got, want)
	}
}
