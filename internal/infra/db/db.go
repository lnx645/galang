// Package db provides database access for GaLang.
package db

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	_ "github.com/go-sql-driver/mysql"
	_ "github.com/lib/pq"
	_ "github.com/mattn/go-sqlite3"
)

// Config holds database configuration.
type Config struct {
	Driver   string
	Host     string
	Port     int
	User     string
	Password string
	Database string
	Params   map[string]string
}

// DSN returns the data source name for the database.
func (c *Config) DSN() string {
	switch c.Driver {
	case "mysql":
		params := ""
		if len(c.Params) > 0 {
			var parts []string
			for k, v := range c.Params {
				parts = append(parts, k+"="+v)
			}
			params = "?" + strings.Join(parts, "&")
		}
		return fmt.Sprintf("%s:%s@tcp(%s:%d)/%s%s",
			c.User, c.Password, c.Host, c.Port, c.Database, params)

	case "postgres":
		return fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=disable",
			c.Host, c.Port, c.User, c.Password, c.Database)

	case "sqlite3":
		return c.Database

	default:
		return ""
	}
}

// DB wraps sql.DB with additional functionality.
type DB struct {
	*sql.DB
	driver string
}

// Open opens a database connection.
func Open(cfg Config) (*DB, error) {
	db, err := sql.Open(cfg.Driver, cfg.DSN())
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	db.SetMaxOpenConns(25)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(5 * time.Minute)

	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	return &DB{DB: db, driver: cfg.Driver}, nil
}

// Query executes a query and returns rows.
func (db *DB) Query(query string, args ...interface{}) (*Rows, error) {
	rows, err := db.DB.Query(query, args...)
	if err != nil {
		return nil, err
	}
	return &Rows{Rows: rows}, nil
}

// QueryRow executes a query that returns a single row.
func (db *DB) QueryRow(query string, args ...interface{}) *Row {
	return &Row{Row: db.DB.QueryRow(query, args...)}
}

// Exec executes a query without returning rows.
func (db *DB) Exec(query string, args ...interface{}) (sql.Result, error) {
	return db.Exec(query, args...)
}

// Transaction represents a database transaction.
type Transaction struct {
	*sql.Tx
	db *DB
}

// Begin starts a new transaction.
func (db *DB) Begin() (*Transaction, error) {
	tx, err := db.DB.Begin()
	if err != nil {
		return nil, err
	}
	return &Transaction{Tx: tx, db: nil}, nil
}

// Commit commits the transaction.
func (t *Transaction) Commit() error {
	return t.Tx.Commit()
}

// Rollback rolls back the transaction.
func (t *Transaction) Rollback() error {
	return t.Tx.Rollback()
}

// Query executes a query within the transaction.
func (t *Transaction) Query(query string, args ...interface{}) (*Rows, error) {
	rows, err := t.Tx.Query(query, args...)
	if err != nil {
		return nil, err
	}
	return &Rows{Rows: rows}, nil
}

// QueryRow executes a query that returns a single row within the transaction.
func (t *Transaction) QueryRow(query string, args ...interface{}) *Row {
	return &Row{Row: t.Tx.QueryRow(query, args...)}
}

// Exec executes a query without returning rows within the transaction.
func (t *Transaction) Exec(query string, args ...interface{}) (sql.Result, error) {
	return t.Tx.Exec(query, args...)
}

// Rows represents a result set.
type Rows struct {
	*sql.Rows
}

// Scan scans the current row into the given destinations.
func (r *Rows) Scan(dest ...interface{}) error {
	return r.Rows.Scan(dest...)
}

// Next advances to the next row.
func (r *Rows) Next() bool {
	return r.Rows.Next()
}

// Close closes the rows.
func (r *Rows) Close() error {
	return r.Rows.Close()
}

// Row represents a single row.
type Row struct {
	*sql.Row
}

// Scan scans the row into the given destinations.
func (r *Row) Scan(dest ...interface{}) error {
	return r.Row.Scan(dest...)
}

// ContextKey is a type for context keys.
type ContextKey string

const (
	// TxKey is the context key for the current transaction.
	TxKey ContextKey = "tx"
)

// WithTx adds a transaction to the context.
func WithTx(ctx context.Context, tx *Transaction) context.Context {
	return context.WithValue(ctx, TxKey, tx)
}

// TxFromContext retrieves the transaction from the context.
func TxFromContext(ctx context.Context) (*Transaction, bool) {
	tx, ok := ctx.Value(TxKey).(*Transaction)
	return tx, ok
}

// QueryContext executes a query with context.
func (db *DB) QueryContext(ctx context.Context, query string, args ...interface{}) (*Rows, error) {
	rows, err := db.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	return &Rows{Rows: rows}, nil
}

// QueryRowContext executes a query that returns a single row with context.
func (db *DB) QueryRowContext(ctx context.Context, query string, args ...interface{}) *Row {
	return &Row{Row: db.DB.QueryRowContext(ctx, query, args...)}
}

// ExecContext executes a query with context.
func (db *DB) ExecContext(ctx context.Context, query string, args ...interface{}) (sql.Result, error) {
	return db.DB.ExecContext(ctx, query, args...)
}

// Transaction runs a function within a transaction.
func (db *DB) Transaction(fn func(*Transaction) error) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer func() {
		if p := recover(); p != nil {
			tx.Rollback()
			panic(p)
		}
	}()
	if err := fn(tx); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}

// QueryRowMap executes a query and returns the first row as a map.
func (db *DB) QueryRowMap(query string, args ...interface{}) (map[string]interface{}, error) {
	rows, err := db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	if !rows.Next() {
		return nil, sql.ErrNoRows
	}

	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}

	values := make([]interface{}, len(cols))
	pointers := make([]interface{}, len(cols))
	for i := range values {
		pointers[i] = &values[i]
	}

	if err := rows.Scan(pointers...); err != nil {
		return nil, err
	}

	result := make(map[string]interface{})
	for i, col := range cols {
		result[col] = values[i]
	}

	return result, nil
}

// QueryMap executes a query and returns all rows as a slice of maps.
func (db *DB) QueryMap(query string, args ...interface{}) ([]map[string]interface{}, error) {
	rows, err := db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}

	var results []map[string]interface{}
	for rows.Next() {
		values := make([]interface{}, len(cols))
		pointers := make([]interface{}, len(cols))
		for i := range values {
			pointers[i] = &values[i]
		}

		if err := rows.Scan(pointers...); err != nil {
			return nil, err
		}

		result := make(map[string]interface{})
		for i, col := range cols {
			result[col] = values[i]
		}
		results = append(results, result)
	}

	return results, nil
}