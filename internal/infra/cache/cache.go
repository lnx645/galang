// Package cache stores the result of parsing a Garurda source file, so that a
// program is not lexed and parsed again on every run or request.
//
// This is the equivalent of PHP's opcache, at the layer where it is possible
// for this engine: the parsed syntax tree is plain data and can be
// serialised, while the compiled form is a tree of Go closures and cannot.
// Caching the tree therefore removes lexing and parsing, which the measurement
// in docs/SPEC.md puts at about two thirds of the compile cost of a typical
// program.
//
// Entries are keyed by a hash of the source text, not by file name and
// timestamp. That makes a stale entry impossible: change one byte of the
// source and the key changes with it. It also means an edited-then-reverted
// file still hits the cache, and two copies of the same program share one entry.
package cache

import (
	"bytes"
	"crypto/sha256"
	"encoding/gob"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"garurda/internal/domain"
	"garurda/internal/usecase/parse"
)

// version guards the file format. Bumping it invalidates every entry, which is
// what should happen when the syntax tree changes.
const version = 1

var magic = [4]byte{'G', 'A', 'R', 'C'}

// Parser obtains programs, reusing cached syntax trees.
type Parser struct {
	// Dir is where entries live. Empty disables the cache.
	Dir string
	// Hits and Misses count lookups, for tests and diagnostics.
	Hits, Misses int
	mu           sync.Mutex
}

// New returns a parser using the default cache directory. The second result is
// false when caching is disabled by the environment.
func New() *Parser {
	if v := os.Getenv("GARURDA_CACHE"); v == "off" || v == "0" {
		return &Parser{}
	}
	dir := os.Getenv("GARURDA_CACHE_DIR")
	if dir == "" {
		base := os.Getenv("XDG_CACHE_HOME")
		if base == "" {
			home, err := os.UserHomeDir()
			if err != nil {
				return &Parser{}
			}
			base = filepath.Join(home, ".cache")
		}
		dir = filepath.Join(base, "garurda", "parse")
	}
	return &Parser{Dir: dir}
}

// Enabled reports whether the cache is in use.
func (p *Parser) Enabled() bool { return p != nil && p.Dir != "" }

// Parse returns the program for src, using the cache when possible.
func (p *Parser) Parse(src, file string) (*parse.Program, error) {
	if !p.Enabled() {
		return parse.Parse(src, file)
	}
	key := hashSource(src)
	if prog, ok := p.load(key, file); ok {
		p.Hits++
		return prog, nil
	}
	p.Misses++
	prog, err := parse.Parse(src, file)
	if err != nil {
		return nil, err
	}
	// A cache miss must never fail the program, so write errors are ignored.
	_ = p.store(key, prog)
	return prog, nil
}

// entry is the serialised form: magic, version, then the statements.
type entry struct {
	Stmts []domain.Stmt
}

func (p *Parser) path(key string) string {
	return filepath.Join(p.Dir, key+".ast")
}

func (p *Parser) load(key, file string) (*parse.Program, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	data, err := os.ReadFile(p.path(key))
	if err != nil {
		return nil, false
	}
	if len(data) < len(magic)+4 || !bytes.Equal(data[:4], magic[:]) {
		return nil, false
	}
	// Skip the magic and read the version separately so a format change is
	// ignored rather than misread.
	vbuf := bytes.NewReader(data[4:8])
	var v uint32
	if err := gob.NewDecoder(vbuf).Decode(&v); err != nil || v != version {
		return nil, false
	}
	var e entry
	if err := gob.NewDecoder(bytes.NewReader(data[8:])).Decode(&e); err != nil {
		// A corrupt entry is treated as a miss; it will be rewritten.
		return nil, false
	}
	return &parse.Program{Stmts: e.Stmts, File: file}, true
}

func (p *Parser) store(key string, prog *parse.Program) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := os.MkdirAll(p.Dir, 0o755); err != nil {
		return err
	}
	var buf bytes.Buffer
	buf.Write(magic[:])
	gob.NewEncoder(&buf).Encode(uint32(version))
	if err := gob.NewEncoder(&buf).Encode(entry{Stmts: prog.Stmts}); err != nil {
		return err
	}
	// Write to a temporary file and rename, so a reader never sees a partial
	// entry even when several processes race to fill the same cache.
	tmp, err := os.CreateTemp(p.Dir, "tmp-*.ast")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(buf.Bytes()); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, p.path(key)); err != nil {
		os.Remove(tmpName)
		return err
	}
	return nil
}

// Clear removes every entry.
func (p *Parser) Clear() error {
	if !p.Enabled() {
		return fmt.Errorf("cache disabled")
	}
	return os.RemoveAll(p.Dir)
}

func hashSource(src string) string {
	sum := sha256.Sum256([]byte(src))
	return hex.EncodeToString(sum[:16])
}

// The syntax tree stores behaviour behind interfaces, so gob needs to be told
// about every concrete node type.
func init() {
	gob.Register(&domain.VarDecl{})
	gob.Register(&domain.AssignStmt{})
	gob.Register(&domain.ExprStmt{})
	gob.Register(&domain.PrintStmt{})
	gob.Register(&domain.ReturnStmt{})
	gob.Register(&domain.IfStmt{})
	gob.Register(&domain.WhileStmt{})
	gob.Register(&domain.ForInStmt{})
	gob.Register(&domain.FnDecl{})
	gob.Register(&domain.BreakStmt{})
	gob.Register(&domain.ContinueStmt{})
	gob.Register(&domain.ThrowStmt{})
	gob.Register(&domain.TryStmt{})
	gob.Register(&domain.UseStmt{})
	gob.Register(&domain.Block{})

	gob.Register(&domain.Ident{})
	gob.Register(&domain.IntLit{})
	gob.Register(&domain.FloatLit{})
	gob.Register(&domain.StrLit{})
	gob.Register(&domain.BoolLit{})
	gob.Register(&domain.NullLit{})
	gob.Register(&domain.ListLit{})
	gob.Register(&domain.ObjectLit{})
	gob.Register(&domain.IndexExpr{})
	gob.Register(&domain.SliceExpr{})
	gob.Register(&domain.PropExpr{})
	gob.Register(&domain.CallExpr{})
	gob.Register(&domain.MethodExpr{})
	gob.Register(&domain.PrefixExpr{})
	gob.Register(&domain.InfixExpr{})
	gob.Register(&domain.RangeExpr{})
	gob.Register(&domain.TernaryExpr{})
	gob.Register(&domain.FnExpr{})
	gob.Register(&domain.GroupExpr{})
}
