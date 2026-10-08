package interp

// Modul `database`: koneksi nyata ke SQLite/MySQL/PostgreSQL lewat
// database/sql. database.connect(dsn) mengembalikan objek dengan metode
// query / query_first / query_row / exec / close; parameter query
//opsional dikirim sebagai array.

import (
	"database/sql"
	"fmt"
	"time"

	"garurda/internal/domain"
)

// dbThrow membangun galat database yang bisa ditangkap try/catch:
// code db_error, status 500.
func (in *Interp) dbThrow(msg string, pos domain.Position) error {
	return in.Throw(msg, "db_error", 500, pos)
}

// dbSQLArgs membaca (sql, [params]?) dari argumen metode koneksi.
func (in *Interp) dbSQLArgs(name string, a []domain.Value, pos domain.Position) (string, []interface{}, error) {
	text, ok := a[0].(domain.Str)
	if !ok {
		return "", nil, in.errf(pos, "$db.%s() expects a SQL string", name)
	}
	if len(a) < 2 {
		return string(text), nil, nil
	}
	switch p := a[1].(type) {
	case domain.Null:
		return string(text), nil, nil
	case *domain.Arr:
		params := make([]interface{}, len(p.Items))
		for i, it := range p.Items {
			params[i] = dbParam(it)
		}
		return string(text), params, nil
	default:
		return "", nil, in.errf(pos, "$db.%s() parameters must be an array, got %s", name, domain.TypeName(a[1]))
	}
}

// dbParam mengubah nilai Garurda menjadi parameter driver.
func dbParam(v domain.Value) interface{} {
	switch x := v.(type) {
	case domain.Str:
		return string(x)
	case domain.Int:
		return int64(x)
	case domain.Float:
		return float64(x)
	case domain.Bool:
		return bool(x)
	case domain.Null:
		return nil
	default:
		return v.String()
	}
}

// dbValue mengubah nilai kolom database menjadi nilai Garurda.
func dbValue(v interface{}) domain.Value {
	switch x := v.(type) {
	case nil:
		return domain.Null{}
	case int64:
		return domain.Int(x)
	case float64:
		return domain.Float(x)
	case bool:
		return domain.Bool(x)
	case []byte:
		return domain.Str(string(x))
	case string:
		return domain.Str(x)
	case time.Time:
		return domain.Str(x.Format(time.RFC3339))
	default:
		return domain.Str(fmt.Sprintf("%v", x))
	}
}

// dbScan menyiapkan target scan dan mengubah satu baris menjadi objek
// dengan nama kolom apa adanya.
func dbScan(cols []string) ([]interface{}, []interface{}) {
	raw := make([]interface{}, len(cols))
	targets := make([]interface{}, len(cols))
	for i := range raw {
		targets[i] = &raw[i]
	}
	return raw, targets
}

// dbRowToObj membangun objek baris dari nilai yang sudah di-scan.
func dbRowToObj(cols []string, raw []interface{}) *domain.Obj {
	obj := domain.NewObj()
	for i, c := range cols {
		obj.Set(c, dbValue(raw[i]))
	}
	return obj
}

// dbRows menjalankan SELECT dan mengembalikan semua baris sebagai
// array objek.
func (in *Interp) dbRows(db *sql.DB, name, query string, params []interface{}, pos domain.Position) (*domain.Arr, error) {
	rows, err := db.Query(query, params...)
	if err != nil {
		return nil, in.dbThrow(err.Error(), pos)
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		return nil, in.dbThrow(err.Error(), pos)
	}
	items := []domain.Value{}
	for rows.Next() {
		raw, targets := dbScan(cols)
		if err := rows.Scan(targets...); err != nil {
			return nil, in.dbThrow(err.Error(), pos)
		}
		items = append(items, dbRowToObj(cols, raw))
	}
	if err := rows.Err(); err != nil {
		return nil, in.dbThrow(err.Error(), pos)
	}
	return domain.NewArr(items...), nil
}

// dbOneRow seperti dbRows tetapi berhenti di baris pertama; null bila
// tidak ada baris.
func (in *Interp) dbOneRow(db *sql.DB, name, query string, params []interface{}, pos domain.Position) (domain.Value, error) {
	rows, err := db.Query(query, params...)
	if err != nil {
		return nil, in.dbThrow(err.Error(), pos)
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		return nil, in.dbThrow(err.Error(), pos)
	}
	if rows.Next() {
		raw, targets := dbScan(cols)
		if err := rows.Scan(targets...); err != nil {
			return nil, in.dbThrow(err.Error(), pos)
		}
		return dbRowToObj(cols, raw), nil
	}
	if err := rows.Err(); err != nil {
		return nil, in.dbThrow(err.Error(), pos)
	}
	return domain.Null{}, nil
}

// dbExec menjalankan INSERT/UPDATE/DELETE dan mengembalikan
// {rows_affected, last_insert_id}. last_insert_id null bila driver
// tidak mendukungnya (mis. PostgreSQL).
func (in *Interp) dbExec(db *sql.DB, name, query string, params []interface{}, pos domain.Position) (domain.Value, error) {
	res, err := db.Exec(query, params...)
	if err != nil {
		return nil, in.dbThrow(err.Error(), pos)
	}
	out := domain.NewObj()
	if n, err := res.RowsAffected(); err == nil {
		out.Set("rows_affected", domain.Int(n))
	} else {
		out.Set("rows_affected", domain.Null{})
	}
	if n, err := res.LastInsertId(); err == nil {
		out.Set("last_insert_id", domain.Int(n))
	} else {
		out.Set("last_insert_id", domain.Null{})
	}
	return out, nil
}

// connObj membungkus *sql.DB menjadi objek metode untuk Garurda:
// $db.query / $db.query_first / $db.query_row / $db.exec / $db.close.
func (in *Interp) connObj(db *sql.DB, driver string) *domain.Obj {
	obj := domain.NewObj()
	obj.Set("driver", domain.Str(driver))
	bind := func(name string, min, max int, fn func(a []domain.Value, pos domain.Position) (domain.Value, error)) {
		obj.Set(name, in.strictFn("database."+name, min, max, func(in *Interp, a []domain.Value, pos domain.Position) (domain.Value, error) {
			return fn(a, pos)
		}))
	}
	bind("query", 1, 2, func(a []domain.Value, pos domain.Position) (domain.Value, error) {
		q, params, err := in.dbSQLArgs("query", a, pos)
		if err != nil {
			return nil, err
		}
		return in.dbRows(db, "query", q, params, pos)
	})
	bind("query_first", 1, 2, func(a []domain.Value, pos domain.Position) (domain.Value, error) {
		q, params, err := in.dbSQLArgs("query_first", a, pos)
		if err != nil {
			return nil, err
		}
		return in.dbOneRow(db, "query_first", q, params, pos)
	})
	bind("query_row", 1, 2, func(a []domain.Value, pos domain.Position) (domain.Value, error) {
		q, params, err := in.dbSQLArgs("query_row", a, pos)
		if err != nil {
			return nil, err
		}
		return in.dbOneRow(db, "query_row", q, params, pos)
	})
	bind("exec", 1, 2, func(a []domain.Value, pos domain.Position) (domain.Value, error) {
		q, params, err := in.dbSQLArgs("exec", a, pos)
		if err != nil {
			return nil, err
		}
		return in.dbExec(db, "exec", q, params, pos)
	})
	bind("close", 0, 0, func(a []domain.Value, pos domain.Position) (domain.Value, error) {
		if err := db.Close(); err != nil {
			return nil, in.dbThrow(err.Error(), pos)
		}
		return domain.Null{}, nil
	})
	return obj
}
