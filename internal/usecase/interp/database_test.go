package interp

import (
	"path/filepath"
	"strings"
	"testing"
)

// Alur SQLite penuh: connect → exec dengan parameter → query →
// query_first → query_row → null bila kosong → close.
func TestDatabaseSqliteAlur(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "tes.db")
	src := `
use "database"
$db = database.connect("sqlite:` + dbPath + `")
print($db.driver)
$db.exec("CREATE TABLE users (id INTEGER PRIMARY KEY, nama TEXT, umur INT, catat TEXT)")
$r = $db.exec("INSERT INTO users (nama, umur, catat) VALUES (?, ?, ?)", ["budi", 30, null])
print($r.rows_affected)
$r2 = $db.exec("INSERT INTO users (nama, umur, catat) VALUES (?, ?, ?)", ["sari", 25, "halo"])
print($r2.last_insert_id)
$rows = $db.query("SELECT id, nama, umur, catat FROM users ORDER BY id")
print(len($rows))
print($rows[0].nama + ":" + str($rows[0].umur) + ":" + str($rows[0].catat))
$one = $db.query_first("SELECT nama FROM users WHERE id = ?", [2])
print($one.nama)
$none = $db.query_first("SELECT nama FROM users WHERE id = ?", [99])
print($none)
$row = $db.query_row("SELECT count(*) AS n FROM users")
print($row.n)
$db.close()
`
	out := mustRun(t, src)
	want := "sqlite3\n1\n2\n2\nbudi:30:null\nsari\nnull\n2"
	if out != want {
		t.Errorf("alur sqlite:\ngot  %q\nwant %q", out, want)
	}
}

// query tanpa parameter array juga sah; parameter non-array adalah
// galat pemrograman (errf, tertangkap hanya oleh Eval — konsisten
// dengan argumen builtin lainnya), bukan db_error.
func TestDatabaseTanpaParameter(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "tes.db")
	src := `
use "database"
$db = database.connect("sqlite:` + dbPath + `")
$db.exec("CREATE TABLE t (v TEXT)")
$rows = $db.query("SELECT 1 AS satu")
print($rows[0].satu)
$db.close()
`
	if out := mustRun(t, src); out != "1" {
		t.Errorf("query tanpa parameter: got %q", out)
	}

	bad := `
use "database"
$db = database.connect("sqlite:` + dbPath + `")
$db.query("SELECT 1", "bukan-array")
`
	_, _, err := runSnippet(t, bad)
	if err == nil {
		t.Fatal("parameter non-array seharusnya ditolak")
	}
	if !strings.Contains(err.Error(), "must be an array") {
		t.Errorf("pesan penolakan tidak jelas: %v", err)
	}
}

// Galat koneksi dan galat SQL melempar db_error yang bisa ditangkap.
func TestDatabaseGalatTertangkap(t *testing.T) {
	src := `
use "database"
try {
    database.connect("postgres://127.0.0.1:1/galang-tidak-ada")
    print("sambung-ok")
} catch e {
    print(e.code)
}
$db = database.connect("sqlite::memory:")
try {
    $db.query("SELEC 1")
    print("tidak-error")
} catch e {
    print(e.code)
}
$db.close()
`
	out := mustRun(t, src)
	if out != "db_error\ndb_error" {
		t.Errorf("galat database: got %q, want dua baris db_error", out)
	}
}
