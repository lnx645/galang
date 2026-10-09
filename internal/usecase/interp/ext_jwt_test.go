//go:build cgo

package interp

// Official ext/jwt extension test: compiles the original source then
// CROSS-verifies HS256/HS384/HS512 signatures against Go's stdlib
// crypto/hmac + crypto/sha* (not just "no error"), plus the error paths:
// wrong secret, tampered payload, alg outside the allowlist (anti
// alg-confusion), exp/nbf claims, object conversion, and decode without
// verification.

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

// bangunJwtC compiles ext/jwt/jwt.c (the original source) into
// <dir>/gne/jwt.so. Skips when gcc is missing — this is our code, not a fixture.
func bangunJwtC(t *testing.T, dir string) {
	t.Helper()
	if _, err := exec.LookPath("gcc"); err != nil {
		t.Skip("gcc not available")
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
		t.Fatalf("cannot compile ext/jwt/jwt.c: %v\n%s", err, b)
	}
}

// hashHs returns the hash function for the algorithm (panics outside the
// three supported HMAC-SHA2 algorithms).
func hashHs(alg string) func() hash.Hash {
	switch alg {
	case "HS256":
		return sha256.New
	case "HS384":
		return sha512.New384
	case "HS512":
		return sha512.New
	}
	panic("unknown algorithm: " + alg)
}

// cekToken verifies a token produced by the extension against Go's
// standard library: its HMAC must be identical (proof the extension's
// SHA-2 + HMAC chain is correct), and the payload must be the claims
// that were signed.
func cekToken(t *testing.T, token, alg, secret, klaim string) {
	t.Helper()
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		t.Fatalf("token %q is not three segments", token)
	}
	mac := hmac.New(hashHs(alg), []byte(secret))
	mac.Write([]byte(parts[0] + "." + parts[1]))
	want := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	if parts[2] != want {
		t.Errorf("%s signature does not match stdlib:\next: %s\nstd: %s",
			alg, parts[2], want)
	}
	hdr, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		t.Fatalf("header is not valid base64url: %v", err)
	}
	if !strings.Contains(string(hdr), `"alg":"`+alg+`"`) {
		t.Errorf("header %q does not contain alg %s", hdr, alg)
	}
	badan, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatalf("payload is not valid base64url: %v", err)
	}
	if string(badan) != klaim {
		t.Errorf("payload = %q, want %q", badan, klaim)
	}
}

// rusakToken flips the first character of the payload segment so a valid
// signature no longer matches (different payload, old MAC).
func rusakToken(t *testing.T, token string) string {
	t.Helper()
	parts := strings.Split(token, ".")
	if len(parts) != 3 || parts[1] == "" {
		t.Fatalf("token %q cannot be corrupted", token)
	}
	if parts[1][0] == 'A' {
		parts[1] = "B" + parts[1][1:]
	} else {
		parts[1] = "A" + parts[1][1:]
	}
	return parts[0] + "." + parts[1] + "." + parts[2]
}

// rusakTapiJsonValid changes one character inside the claims so the
// payload stays valid JSON but the signature no longer matches —
// separating the "invalid signature" (401) path from the "payload is not
// JSON" (400) path.
func rusakTapiJsonValid(t *testing.T, token string) string {
	t.Helper()
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		t.Fatalf("token %q is not three segments", token)
	}
	b, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatalf("payload is not valid: %v", err)
	}
	baru := bytes.Replace(b, []byte("budi"), []byte("budx"), 1)
	if string(baru) == string(b) {
		t.Fatalf("claims %q do not contain 'budi' to corrupt", b)
	}
	return parts[0] + "." + base64.RawURLEncoding.EncodeToString(baru) + "." + parts[2]
}

// tokenManual assembles a raw token from header+claims+secret with Go's
// stdlib (used for "alg:none"/RS256 tokens outside the allowlist and for
// the expired token in the decode-without-verification test).
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

// TestJwtRoundTripThroughGo signs with the extension in all three
// algorithms, then verifies the tokens with Go's crypto/hmac — the
// extension's SHA-2/HMAC chain is proven identical to a trusted
// implementation. Also proves the object API: sign() accepts a GaLang
// object directly (serialized in C), and verify()/decode() return
// objects.
func TestJwtRoundTripThroughGo(t *testing.T) {
	dir := t.TempDir()
	bangunJwtC(t, dir)

	// Byte-exact JSON the C serializer must emit for the object below.
	klaim := `{"sub":"budi","role":"admin"}`
	out, err := jalankanMain(t, dir, fmt.Sprintf(`
use "jwt"
$t256 = jwt.sign({"sub": "budi", "role": "admin"}, "rahasia", "HS256")
print($t256)
print(jwt.verify($t256, "rahasia"))
$t384 = jwt.sign({"sub": "budi", "role": "admin"}, "rahasia", "HS384")
print($t384)
print(jwt.verify($t384, "rahasia"))
$t512 = jwt.sign({"sub": "budi", "role": "admin"}, "rahasia", "HS512")
print($t512)
print(jwt.verify($t512, "rahasia"))
$tdef = jwt.sign({"sub": "budi", "role": "admin"}, "rahasia")
print($tdef)
print(jwt.decode($t256))
print(jwt.decode($tdef))
$tstr = jwt.sign(%q, "rahasia")
print($tstr)
print(jwt.verify($tstr, "rahasia").sub)
`, klaim))
	if err != nil {
		t.Fatalf("eval: %v", err)
	}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 11 {
		t.Fatalf("line count = %d, want 11:\n%s", len(lines), out)
	}
	// Tokens built by the extension, re-verified by Go's stdlib.
	cekToken(t, lines[0], "HS256", "rahasia", klaim)
	cekToken(t, lines[2], "HS384", "rahasia", klaim)
	cekToken(t, lines[4], "HS512", "rahasia", klaim)
	cekToken(t, lines[6], "HS256", "rahasia", klaim) // default is HS256
	cekToken(t, lines[9], "HS256", "rahasia", klaim) // raw string claims still work
	// verify()/decode() return objects; the object sign() received was
	// serialized in insertion order with compact JSON.
	for _, i := range []int{1, 3, 5, 7, 8} {
		if lines[i] != "{sub: budi, role: admin}" {
			t.Errorf("line %d = %q, want %q", i+1, lines[i], "{sub: budi, role: admin}")
		}
	}
	if lines[10] != "budi" {
		t.Errorf("field access line = %q, want %q", lines[10], "budi")
	}
}

// TestJwtSignObjectTypes round-trips every JSON value type through
// sign(object) → decode/verify: the C serializer must produce compact
// JSON whose values survive the C parser unchanged (escapes, unicode,
// numbers, nesting).
func TestJwtSignObjectTypes(t *testing.T) {
	dir := t.TempDir()
	bangunJwtC(t, dir)

	out, err := jalankanMain(t, dir, `
use "jwt"
$t = jwt.sign({"s": "ka\"fe ☕\n", "n": 42, "f": 1.5, "b": true, "x": null, "arr": [1, 2, 3], "o": {"k": "v"}, "deep": [{"z": [true]}]}, "rahasia")
$v = jwt.verify($t, "rahasia")
print($v.s == "ka\"fe ☕\n")
print($v.n + 1)
print($v.f)
print($v.b)
print($v.x)
print($v.arr[1])
print($v.o.k)
print($v.deep[0].z[0])
print($v.x == null)
`)
	if err != nil {
		t.Fatalf("eval: %v", err)
	}
	want := strings.Join([]string{
		"true", // escaped quote/newline + raw unicode round-trip
		"43",
		"1.5",
		"true",
		"null",
		"2",
		"v",
		"true",
		"true",
	}, "\n")
	if got := strings.TrimRight(out, "\n"); got != want {
		t.Errorf("output wrong:\ngot:\n%s\nwant:\n%s", got, want)
	}
}

// TestJwtErrors exercises every error path: wrong secret, tampered
// payload, alg outside the allowlist, exp/nbf claims, and wrong
// arguments.
func TestJwtErrors(t *testing.T) {
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
$t = jwt.sign({"sub": "budi"}, "rahasia", "HS256")
try {
  jwt.verify($t, "salah")
  print("no")
} catch e {
  print(e.code + "/" + str(e.status))
}
try {
  jwt.verify(%q, "rahasia")
  print("no")
} catch e {
  print(e.code + "/" + str(e.status))
}
try {
  jwt.verify(%q, "rahasia")
  print("no")
} catch e {
  print(e.code + "/" + str(e.status))
}
try {
  jwt.verify(%q, "rahasia")
  print("no")
} catch e {
  print(e.code + "/" + str(e.status))
}
try {
  jwt.verify(%q, "rahasia")
  print("no")
} catch e {
  print(e.code + "/" + str(e.status))
}
try {
  jwt.verify(%q, "rahasia")
  print("no")
} catch e {
  print(e.code + "/" + str(e.status))
}
try {
  jwt.verify("notvalid", "rahasia")
  print("no")
} catch e {
  print(e.code + "/" + str(e.status))
}
try {
  jwt.decode("abc.d ef")
  print("no")
} catch e {
  print(e.code + "/" + str(e.status))
}
$e1 = jwt.sign({"exp": 1000000000}, "rahasia")
try {
  jwt.verify($e1, "rahasia")
  print("no")
} catch e {
  print(e.code + "/" + str(e.status))
}
$n1 = jwt.sign({"nbf": 4102444800}, "rahasia")
try {
  jwt.verify($n1, "rahasia")
  print("no")
} catch e {
  print(e.code + "/" + str(e.status))
}
$es = jwt.sign({"exp": "later"}, "rahasia")
try {
  jwt.verify($es, "rahasia")
  print("no")
} catch e {
  print(e.code + "/" + str(e.status))
}
try {
  jwt.sign(123, "rahasia")
  print("no")
} catch e {
  print(e.code)
}
$ok = jwt.sign({"exp": 4102444800}, "rahasia")
print(jwt.verify($ok, "rahasia"))
`, rusakJson, rusakValid, algNoneKosong, algNoneSignature,
		algRs))
	if err != nil {
		t.Fatalf("eval: %v", err)
	}

	want := strings.Join([]string{
		"jwt_error/401",     // wrong secret
		"jwt_error/400",     // payload not a JSON object (assessed before the MAC)
		"jwt_error/401",     // payload still JSON but the MAC does not match
		"jwt_error/400",     // alg:none without signature → format rejected
		"jwt_error/401",     // alg:none with signature → outside the allowlist
		"jwt_error/401",     // RS256 outside the allowlist
		"jwt_error/400",     // single-segment token
		"jwt_error/400",     // decode: segment not base64url
		"jwt_error/401",     // exp in the past
		"jwt_error/401",     // nbf in the future
		"jwt_error/400",     // exp is not a number
		"type_error",        // non-object, non-string claims
		"{exp: 4102444800}", // far-future exp → verify succeeds, object returned
	}, "\n")
	if got := strings.TrimRight(out, "\n"); got != want {
		t.Errorf("output wrong:\ngot:\n%s\nwant:\n%s", got, want)
	}
}

// TestJwtDecodeWithoutVerification ensures decode returns the claims as
// an object without checking the signature or validity period — for
// reading tokens that are verified separately.
func TestJwtDecodeWithoutVerification(t *testing.T) {
	dir := t.TempDir()
	bangunJwtC(t, dir)

	kedaluwarsa := tokenManual(`{"alg":"HS256","typ":"JWT"}`,
		`{"exp":1000000000}`, "rahasia")
	// Even a fake signature does not stop decode — decode is
	// verification-free by design (only read tokens you verify separately).
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
		`{exp: 1000000000}`,
		`{sub: budi}`,
	}, "\n")
	if got := strings.TrimRight(out, "\n"); got != want {
		t.Errorf("decode output wrong:\ngot:\n%s\nwant:\n%s", got, want)
	}
}
