// Package template menyediakan mesin template Blade untuk Garurda.
//
// Model render:
//
//  1. Muat file entry, telusuri rantai @extends ke atas, dan kumpulkan
//     semua partial @include (BFS, dengan penjaga siklus).
//  2. Kumpulkan default @yield("slot", "nilai") dari file layout →
//     didefinisikan lebih dahulu sehingga section child selalu menang.
//  3. Parse ke satu set template Go: default → include → layout
//     (terluar lebih dahulu) → entry terakhir (root body).
//  4. Execute root; body entry memanggil layout lewat {{template ...}},
//     layout memanggil section child lewat @yield.
//
// Konversi sintaks Blade dilakukan oleh convert() (lihat expr.go untuk
// penerjemah ekspresi).
package template

import (
	"encoding/json"
	"fmt"
	"html"
	"html/template"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Engine adalah mesin template Blade.
type Engine struct {
	viewsDir string
	mu       sync.RWMutex
	cache    map[string]*cachedFile
}

// cachedFile adalah sumber template + mtime file untuk invalidasi cache.
type cachedFile struct {
	source string
	mtime  time.Time
}

// NewEngine membuat mesin template yang membaca file dari viewsDir.
func NewEngine(viewsDir string) *Engine {
	return &Engine{
		viewsDir: viewsDir,
		cache:    make(map[string]*cachedFile),
	}
}

// refSrc adalah sepasang nama rujukan (sesuai tertulis di direktif)
// dan sumber file-nya.
type refSrc struct {
	ref    string
	source string
}

// gathered adalah hasil pengumpulan file yang terlibat dalam satu render.
type gathered struct {
	entry    string // nama file entry (untuk pesan error)
	src      string // sumber entry
	layouts  []refSrc
	includes []refSrc
	defaults map[string]string // nama slot @yield → ekspresi default ("" = kosong)
}

var (
	extendsRe  = regexp.MustCompile(`@extends\s*\(\s*['"]([^'"]+)['"]\s*\)`)
	includeRe  = regexp.MustCompile(`@include\s*\(\s*['"]([^'"]+)['"]`)
	yieldStart = regexp.MustCompile(`@yield\s*\(\s*['"]([^'"]+)['"]`)
)

// Render merender template entry name dengan data.
func (e *Engine) Render(name string, data map[string]interface{}) (string, error) {
	g, err := e.gather(name)
	if err != nil {
		return "", err
	}

	t := template.New("blade").Funcs(funcMap())

	// 1. Default sintetis untuk setiap slot @yield — paling awal supaya
	//    section child (diparse belakangan) selalu menang.
	if piece := defaultsPiece(g.defaults); piece != "" {
		if _, err := t.Parse(piece); err != nil {
			return "", fmt.Errorf("template %s: %w", name, err)
		}
	}

	// 2. Partial @include, masing-masing jadi subtemplate bernama.
	for _, f := range g.includes {
		sub := t.New(f.ref)
		if _, err := sub.Parse(convert(f.source, newScope())); err != nil {
			return "", fmt.Errorf("template %s: %w", f.ref, err)
		}
	}

	// 3. Layout: rantai child→parent, parse dari terluar ke terdekat
	//    supaya section yang lebih dekat dengan child menang.
	for i := len(g.layouts) - 1; i >= 0; i-- {
		f := g.layouts[i]
		sub := t.New(f.ref)
		if _, err := sub.Parse(convert(f.source, newScope())); err != nil {
			return "", fmt.Errorf("template %s: %w", f.ref, err)
		}
	}

	// 4. Entry terakhir — root body memegang konten child dan pemanggilan
	//    layout dari @extends.
	if _, err := t.Parse(convert(g.src, newScope())); err != nil {
		return "", fmt.Errorf("template %s: %w", name, err)
	}

	var buf strings.Builder
	if err := t.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("template %s: %w", name, err)
	}
	return buf.String(), nil
}

// gather memuat entry, menelusuri rantai @extends, mengumpulkan partial
// @include, dan mencatat default @yield.
func (e *Engine) gather(entry string) (*gathered, error) {
	entryPath, err := e.resolvePath(entry)
	if err != nil {
		return nil, err
	}
	src, err := e.loadFile(entry)
	if err != nil {
		return nil, err
	}

	g := &gathered{entry: entry, src: src, defaults: make(map[string]string)}
	visited := map[string]bool{entryPath: true}

	// Rantai @extends: entry → L1 → L2 → ...
	cur := refSrc{ref: entry, source: src}
	for depth := 0; ; depth++ {
		m := extendsRe.FindStringSubmatch(cur.source)
		if m == nil {
			break
		}
		if depth >= 16 {
			return nil, fmt.Errorf("template %s: terlalu banyak level @extends", entry)
		}
		target := m[1]
		tpath, err := e.resolvePath(target)
		if err != nil {
			return nil, fmt.Errorf("template %s: @extends: %w", cur.ref, err)
		}
		if visited[tpath] {
			return nil, fmt.Errorf("template %s: @extends melingkar ke %q", entry, target)
		}
		visited[tpath] = true
		tsrc, err := e.loadFile(target)
		if err != nil {
			return nil, fmt.Errorf("template %s: @extends: %w", cur.ref, err)
		}
		g.layouts = append(g.layouts, refSrc{ref: target, source: tsrc})
		cur = refSrc{ref: target, source: tsrc}
	}

	// Partial: BFS dari entry dan semua layout.
	includeReAll := regexp.MustCompile(`@include\s*\(\s*['"]([^'"]+)['"]`)
	queue := append([]refSrc{{ref: entry, source: src}}, g.layouts...)
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, m := range includeReAll.FindAllStringSubmatch(cur.source, -1) {
			target := m[1]
			tpath, err := e.resolvePath(target)
			if err != nil {
				return nil, fmt.Errorf("template %s: @include: %w", cur.ref, err)
			}
			if visited[tpath] {
				continue
			}
			visited[tpath] = true
			tsrc, err := e.loadFile(target)
			if err != nil {
				return nil, fmt.Errorf("template %s: @include: %w", cur.ref, err)
			}
			f := refSrc{ref: target, source: tsrc}
			g.includes = append(g.includes, f)
			queue = append(queue, f)
		}
	}

	// Default @yield dari semua file yang terlibat.
	scan := append([]refSrc{{ref: entry, source: src}}, g.layouts...)
	scan = append(scan, g.includes...)
	for _, f := range scan {
		collectYieldDefaults(f.source, g.defaults)
	}
	return g, nil
}

// collectYieldDefaults mencatat ekspresi default tiap @yield("slot", x).
// Slot tanpa default tetap dicatat dengan "" supaya tidak pernah
// "template not defined" saat execute.
func collectYieldDefaults(src string, out map[string]string) {
	for _, idx := range findIndices(src, yieldStart) {
		m := yieldStart.FindStringSubmatch(src[idx:])
		if m == nil {
			continue
		}
		name := m[1]
		open := idx + strings.Index(src[idx:], "(")
		inner, _, ok := matchParen(src, open)
		if !ok {
			continue
		}
		// inner: "slot" [, ekspresi]
		if comma := strings.Index(inner, ","); comma >= 0 {
			if _, exists := out[name]; !exists {
				out[name] = strings.TrimSpace(inner[comma+1:])
			}
		} else if _, exists := out[name]; !exists {
			out[name] = ""
		}
	}
}

// findIndices mengembalikan posisi awal setiap kecocokan re di src.
func findIndices(src string, re *regexp.Regexp) []int {
	var idx []int
	for _, loc := range re.FindAllStringIndex(src, -1) {
		idx = append(idx, loc[0])
	}
	return idx
}

// defaultsPiece membangun potongan parse berisi {{define}} default.
func defaultsPiece(defaults map[string]string) string {
	var sb strings.Builder
	for name, expr := range defaults {
		// Body dibungkus aksi {{ ... }} supaya string literal tidak
		// tampil bersama tanda kutipnya.
		body := ""
		if expr != "" {
			body = "{{" + translateExpr(expr, newScope()) + "}}"
		}
		sb.WriteString(`{{define "`)
		sb.WriteString(name)
		sb.WriteString(`"}}`)
		sb.WriteString(body)
		sb.WriteString(`{{end}}`)
	}
	return sb.String()
}

// resolvePath mengubah rujukan direktif menjadi path file. Rujukan tanpa
// ekstensi mendapat akhiran .blade.
func (e *Engine) resolvePath(ref string) (string, error) {
	fname := ref
	if filepath.Ext(fname) == "" {
		fname += ".blade"
	}
	path := filepath.Clean(filepath.Join(e.viewsDir, fname))
	// Path harus tetap di dalam viewsDir (tolak rujukan keluar).
	rel, err := filepath.Rel(e.viewsDir, path)
	if err != nil || strings.HasPrefix(rel, "..") {
		return "", fmt.Errorf("path template di luar direktori views: %q", ref)
	}
	return path, nil
}

// loadFile membaca file template dengan cache berbasis mtime.
func (e *Engine) loadFile(ref string) (string, error) {
	path, err := e.resolvePath(ref)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("template not found: %s", ref)
	}

	e.mu.RLock()
	c, ok := e.cache[path]
	e.mu.RUnlock()
	if ok && c.mtime.Equal(info.ModTime()) {
		return c.source, nil
	}

	data, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return "", fmt.Errorf("template not found: %s", ref)
	}

	e.mu.Lock()
	e.cache[path] = &cachedFile{source: string(data), mtime: info.ModTime()}
	e.mu.Unlock()
	return string(data), nil
}

// funcMap mengembalikan fungsi yang tersedia di dalam template Blade.
func funcMap() template.FuncMap {
	return template.FuncMap{
		"raw": func(v interface{}) template.HTML {
			return template.HTML(fmt.Sprint(v))
		},
		"tojson": func(v interface{}) string {
			b, _ := json.Marshal(v)
			return string(b)
		},
		"escape": html.EscapeString,
		"upper":  strings.ToUpper,
		"lower":  strings.ToLower,
		"title":  strings.Title,
		"trim":   strings.TrimSpace,
		"isset":  func(v interface{}) bool { return v != nil },
		"count":  templateCount,
		// Literal object hasil terjemahan {k: v} → dict "k" v.
		"dict": func(args ...interface{}) map[string]interface{} {
			m := make(map[string]interface{}, len(args)/2)
			for i := 0; i+1 < len(args); i += 2 {
				m[fmt.Sprint(args[i])] = args[i+1]
			}
			return m
		},
		// Operator aritmetika hasil terjemahan ekspresi ($a + $b).
		"add": templateAdd,
		"sub": templateNum2(func(a, b float64) float64 { return a - b }),
		"mul": func(args ...interface{}) interface{} {
			r := 1.0
			float := false
			for _, a := range args {
				f, ok := toFloat(a)
				if !ok {
					return fmt.Sprint(args...)
				}
				if !isIntValue(a) {
					float = true
				}
				r *= f
			}
			if !float && r == float64(int64(r)) {
				return int64(r)
			}
			return r
		},
		"div": func(a, b interface{}) (interface{}, error) {
			fa, ok1 := toFloat(a)
			fb, ok2 := toFloat(b)
			if !ok1 || !ok2 {
				return nil, fmt.Errorf("div: bukan angka")
			}
			if fb == 0 {
				return nil, fmt.Errorf("pembagian nol")
			}
			return fa / fb, nil
		},
	}
}

// templateCount menghitung panjang array/object/string (untuk Blade count()).
func templateCount(v interface{}) int {
	if v == nil {
		return 0
	}
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Array, reflect.Slice, reflect.Map, reflect.String, reflect.Chan:
		return rv.Len()
	default:
		return 0
	}
}

// templateAdd menjumlahkan angka, atau menggabungkan bila ada string
// (mengikuti perilaku + pada string di bahasa Garurda).
func templateAdd(args ...interface{}) interface{} {
	hasStr := false
	for _, a := range args {
		if _, isStr := a.(string); isStr {
			hasStr = true
		}
	}
	if hasStr {
		var sb strings.Builder
		for _, a := range args {
			sb.WriteString(fmt.Sprint(a))
		}
		return sb.String()
	}
	var sum float64
	float := false
	for _, a := range args {
		f, ok := toFloat(a)
		if !ok {
			return fmt.Sprint(args...)
		}
		if !isIntValue(a) {
			float = true
		}
		sum += f
	}
	if !float && sum == float64(int64(sum)) {
		return int64(sum)
	}
	return sum
}

// templateNum2 membangun biner numerik: sub(a, b).
func templateNum2(f func(a, b float64) float64) func(a, b interface{}) (interface{}, error) {
	return func(a, b interface{}) (interface{}, error) {
		fa, ok1 := toFloat(a)
		fb, ok2 := toFloat(b)
		if !ok1 || !ok2 {
			return nil, fmt.Errorf("bukan angka")
		}
		r := f(fa, fb)
		if isIntValue(a) && isIntValue(b) && r == float64(int64(r)) {
			return int64(r), nil
		}
		return r, nil
	}
}

func toFloat(v interface{}) (float64, bool) {
	switch n := v.(type) {
	case int:
		return float64(n), true
	case int32:
		return float64(n), true
	case int64:
		return float64(n), true
	case float32:
		return float64(n), true
	case float64:
		return n, true
	default:
		return 0, false
	}
}

func isIntValue(v interface{}) bool {
	switch v.(type) {
	case int, int32, int64:
		return true
	default:
		return false
	}
}
