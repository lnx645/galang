// Package template provides the Blade template engine for Garurda.
package template

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html"
	"html/template"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Engine is the Blade template engine.
type Engine struct {
	viewsDir string
	cache    map[string]*Template
	mu       sync.RWMutex
	funcMap  map[string]interface{}
}

// Template represents a compiled Blade template.
type Template struct {
	name     string
	source   string
	compiled []byte
	mtime    time.Time
}

// NewEngine creates a new template engine.
func NewEngine(viewsDir string) *Engine {
	return &Engine{
		viewsDir: viewsDir,
		cache:    make(map[string]*Template),
		funcMap:  defaultFuncMap(),
	}
}

// defaultFuncMap returns the default template functions.
func defaultFuncMap() map[string]interface{} {
	return map[string]interface{}{
		"escape": html.EscapeString,
		"raw":    func(s string) string { return s },
		"upper":  strings.ToUpper,
		"lower":  strings.ToLower,
		"title":  strings.Title,
		"trim":   strings.TrimSpace,
		"len":    func(v interface{}) int { return 0 }, // placeholder
	}
}

// Render renders a template with the given data.
func (e *Engine) Render(name string, data map[string]interface{}) (string, error) {
	tmpl, err := e.loadTemplate(name)
	if err != nil {
		return "", err
	}
	return tmpl.Execute(data)
}

// loadTemplate loads a template from cache or filesystem.
func (e *Engine) loadTemplate(name string) (*Template, error) {
	e.mu.RLock()
	tmpl, ok := e.cache[name]
	e.mu.RUnlock()

	if ok {
		// Check if file has been modified
		path := filepath.Join(e.viewsDir, name)
		if info, err := os.Stat(path); err == nil && info.ModTime().Equal(tmpl.mtime) {
			return tmpl, nil
		}
	}

	// Load and compile template
	e.mu.Lock()
	defer e.mu.Unlock()

	// Double-check after acquiring write lock
	if tmpl, ok := e.cache[name]; ok {
		path := filepath.Join(e.viewsDir, name)
		if info, err := os.Stat(path); err == nil && info.ModTime().Equal(tmpl.mtime) {
			return tmpl, nil
		}
	}

	path := filepath.Join(e.viewsDir, name)
	source, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return nil, fmt.Errorf("template not found: %s", name)
	}

	tmpl = &Template{
		name:   name,
		source: string(source),
		mtime:  time.Now(),
	}
	if err := tmpl.compile(); err != nil {
		return nil, err
	}

	e.cache[name] = tmpl
	return tmpl, nil
}

// compile compiles the Blade template to Go code.
func (t *Template) compile() error {
	var buf bytes.Buffer
	buf.WriteString("package main\n\n")
	buf.WriteString("import (\n")
	buf.WriteString("\t\"bytes\"\n")
	buf.WriteString("\t\"html\"\n")
	buf.WriteString("\t\"html/template\"\n")
	buf.WriteString("\t\"fmt\"\n")
	buf.WriteString(")\n\n")
	buf.WriteString("func execute(data map[string]interface{}) (string, error) {\n")
	buf.WriteString("\tvar buf bytes.Buffer\n")
	buf.WriteString("\tt := template.Must(template.New(\"t\").Parse(`")

	// Convert Blade syntax to Go template
	goSource := convertBladeToGoTemplate(string(t.source))
	buf.WriteString(goSource)

	buf.WriteString("`))\n")
	buf.WriteString("\treturn buf.String(), t.Execute(&buf, data)\n")
	buf.WriteString("}\n")

	t.compiled = buf.Bytes()
	return nil
}

// convertBladeToGoTemplate converts Blade syntax to Go template syntax.
func convertBladeToGoTemplate(source string) string {
	// Handle @extends
	source = regexp.MustCompile(`@extends\s*\(\s*['"]([^'"]+)['"]\s*\)`).ReplaceAllString(source, `{{define "content"}}{{template "`)

	// Handle @section
	source = regexp.MustCompile(`@section\s*\(\s*['"]([^'"]+)['"]\s*\)`).ReplaceAllString(source, `{{define "$1"}}`)

	// Handle @endsection
	source = regexp.MustCompile(`@endsection`).ReplaceAllString(source, `{{end}}`)

	// Handle @yield
	source = regexp.MustCompile(`@yield\s*\(\s*['"]([^'"]+)['"]\s*\)`).ReplaceAllString(source, `{{template "$1" .}}`)

	// Handle @if / @else / @elseif / @endif
	source = regexp.MustCompile(`@if\s*\(([^)]+)\)`).ReplaceAllString(source, `{{if $1}}`)
	source = regexp.MustCompile(`@elseif\s*\(([^)]+)\)`).ReplaceAllString(source, `{{else if $1}}`)
	source = regexp.MustCompile(`@else`).ReplaceAllString(source, `{{else}}`)
	source = regexp.MustCompile(`@endif`).ReplaceAllString(source, `{{end}}`)

	// Handle @foreach / @endforeach
	source = regexp.MustCompile(`@foreach\s*\(([^)]+)\s+as\s+\$([^)]+)\)`).ReplaceAllString(source, `{{range $1}}`)
	source = regexp.MustCompile(`@endforeach`).ReplaceAllString(source, `{{end}}`)

	// Handle @for
	source = regexp.MustCompile(`@for\s*\(([^;]+);\s*([^;]+);\s*([^)]+)\)`).ReplaceAllString(source, `{{range $1}}`)

	// Handle @foreach with key
	source = regexp.MustCompile(`@foreach\s*\(([^)]+)\s+as\s+\$([^=>]+)\s*=>\s*\$([^)]+)\)`).ReplaceAllString(source, `{{range $1}}`)

	// Handle @while / @endwhile
	source = regexp.MustCompile(`@while\s*\(([^)]+)\)`).ReplaceAllString(source, `{{range $1}}`)
	source = regexp.MustCompile(`@endwhile`).ReplaceAllString(source, `{{end}}`)

	// Handle @forelse / @empty / @endforelse
	source = regexp.MustCompile(`@forelse\s*\(([^)]+)\s+as\s+\$([^)]+)\)`).ReplaceAllString(source, `{{if $1}}{{range $1}}`)
	source = regexp.MustCompile(`@empty`).ReplaceAllString(source, `{{else}}`)
	source = regexp.MustCompile(`@endforelse`).ReplaceAllString(source, `{{end}}{{end}}`)

	// Handle @include
	source = regexp.MustCompile(`@include\s*\(\s*['"]([^'"]+)['"]\s*\)`).ReplaceAllString(source, `{{template "$1" .}}`)

	// Handle @include with data
	source = regexp.MustCompile(`@include\s*\(\s*['"]([^'"]+)['"]\s*,\s*([^)]+)\)`).ReplaceAllString(source, `{{template "$1" $2}}`)

	// Handle {{ }} (escaped)
	source = regexp.MustCompile(`\{\{\s*([^}]+)\s*\}\}`).ReplaceAllString(source, `{{. | html}}`)

	// Handle {!! !!} (unescaped)
	source = regexp.MustCompile(`\{\{\!\s*([^}]+)\s*\!\}\}`).ReplaceAllString(source, `{{$1}}`)

	// Handle {{-- --}} comments
	source = regexp.MustCompile(`\{\{--\s*([^}]+)\s*--\}\}`).ReplaceAllString(source, ``)

	// Handle @json
	source = regexp.MustCompile(`@json\s*\(([^)]+)\)`).ReplaceAllString(source, `{{$1 | tojson}}`)

	// Handle @csrf
	source = regexp.MustCompile(`@csrf`).ReplaceAllString(source, `<input type="hidden" name="_token" value="{{.csrf_token}}">`)

	// Handle @method
	source = regexp.MustCompile(`@method\s*\(\s*['"]([^'"]+)['"]\s*\)`).ReplaceAllString(source, `<input type="hidden" name="_method" value="$1">`)

	// Handle @isset / @empty
	source = regexp.MustCompile(`@isset\s*\(([^)]+)\)`).ReplaceAllString(source, `{{if $1}}`)
	source = regexp.MustCompile(`@empty\s*\(([^)]+)\)`).ReplaceAllString(source, `{{if not $1}}`)
	source = regexp.MustCompile(`@endisset`).ReplaceAllString(source, `{{end}}`)
	source = regexp.MustCompile(`@endempty`).ReplaceAllString(source, `{{end}}`)

	// Handle @php / @endphp (not supported in Go template, strip)
	source = regexp.MustCompile(`@php\s*[\s\S]*?@endphp`).ReplaceAllString(source, ``)

	// Convert $variable to .variable
	source = regexp.MustCompile(`\$([a-zA-Z_][a-zA-Z0-9_]*)`).ReplaceAllString(source, `.$1`)

	// Handle method calls on variables
	source = regexp.MustCompile(`\.([a-zA-Z_][a-zA-Z0-9_]*)\s*\(`).ReplaceAllString(source, `.`)

	return source
}

// Execute executes the compiled template with the given data.
func (t *Template) Execute(data map[string]interface{}) (string, error) {
	// For now, use a simple approach with Go's text/template
	// In production, this would use the compiled Go code
	tmpl := template.Must(template.New(t.name).Parse(t.source))
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// Helper functions for template
func toJSON(v interface{}) string {
	b, _ := json.Marshal(v)
	return string(b)
}