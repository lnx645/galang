// Package dbsql membuka koneksi database untuk modul `database`.
// Tiga driver didaftarkan di sini (blank import): sqlite3 (butuh CGO),
// mysql, dan postgres (pure Go).
package dbsql

import (
	"database/sql"
	"strings"

	_ "github.com/go-sql-driver/mysql"
	_ "github.com/lib/pq"
	_ "github.com/mattn/go-sqlite3"
)

// Open membuka koneksi sesuai prefiks DSN dan memastikannya hidup
// (Ping). Prefiks yang dikenal:
//
//	sqlite:... / path biasa → sqlite3 (file, atau :memory:)
//	mysql:... / mysql://... → mysql
//	postgres://... / postgresql://... / postgres:... → postgres
//
// Prefiks dilepas sebelum diserahkan ke driver; sisanya diteruskan
// apa adanya.
func Open(dsn string) (*sql.DB, string, error) {
	driver, target := splitDSN(dsn)
	db, err := sql.Open(driver, target)
	if err != nil {
		return nil, driver, err
	}
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, driver, err
	}
	return db, driver, nil
}

// splitDSN memisahkan nama driver dari isi DSN.
func splitDSN(dsn string) (driver, target string) {
	switch {
	case strings.HasPrefix(dsn, "mysql://"):
		return "mysql", dsn[len("mysql://"):]
	case strings.HasPrefix(dsn, "mysql:"):
		return "mysql", dsn[len("mysql:"):]
	case strings.HasPrefix(dsn, "postgres://"),
		strings.HasPrefix(dsn, "postgresql://"),
		strings.HasPrefix(dsn, "postgres:"):
		// lib/pq menerima URL postgres:// maupun postgresql:// dan
		// DSN kata kunci (host=... user=...) apa adanya.
		return "postgres", dsn
	case strings.HasPrefix(dsn, "sqlite://"):
		return "sqlite3", dsn[len("sqlite://"):]
	case strings.HasPrefix(dsn, "sqlite:"):
		return "sqlite3", dsn[len("sqlite:"):]
	default:
		return "sqlite3", dsn
	}
}
