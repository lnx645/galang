// Penerjemah sintaks Blade → sintaks template Go.
//
// Template Go tidak punya operator infix: `{{ $a + $b }}` atau
// `@if($x > 3)` adalah parse error. Karena itu setiap ekspresi diterjemahkan
// ke bentuk prefix: `add .a .b`, `gt .x 3`, `and (eq .a 1) (eq .b 2)`.
//
// Jika ada bagian ekspresi yang tidak bisa diterjemahkan, terjemahan
// dibatalkan dan hanya konversi variabel ($x → .x, di luar string) yang
// dipakai — hasilnya tidak pernah lebih buruk daripada tanpa penerjemah.
package template

import "strings"

// ---------------------------------------------------------------------------
// Scope variabel terikat (foreach)

type scope struct {
	frames []map[string]bool
}

func newScope() *scope { return &scope{} }

func (s *scope) push(names ...string) {
	m := make(map[string]bool, len(names))
	for _, n := range names {
		m[n] = true
	}
	s.frames = append(s.frames, m)
}

func (s *scope) pop() {
	if len(s.frames) > 0 {
		s.frames = s.frames[:len(s.frames)-1]
	}
}

func (s *scope) bound(name string) bool {
	for i := len(s.frames) - 1; i >= 0; i-- {
		if s.frames[i][name] {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// Token

type tokKind int

const (
	tokEOF tokKind = iota
	tokNum
	tokStr    // literal string dengan tanda kutip asli
	tokField  // .a.b
	tokVar    // $terikat (dengan rantai .field opsional)
	tokIdent  // nama fungsi / true / false / null
	tokOp     // operator dan tanda kurung
)

type token struct {
	kind tokKind
	val  string
}

// lexExpr memecah ekspresi Blade menjadi token. Variabel sudah dikonversi
// sesuai scope: terikat → $x, bebas → .x.
func lexExpr(s string, sc *scope) ([]token, bool) {
	var toks []token
	i := 0
	isIdentStart := func(c byte) bool {
		return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
	}
	isIdentChar := func(c byte) bool {
		return isIdentStart(c) || (c >= '0' && c <= '9')
	}
	for i < len(s) {
		c := s[i]
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			i++
		case c >= '0' && c <= '9':
			j := i
			for j < len(s) && ((s[j] >= '0' && s[j] <= '9') || s[j] == '.') {
				j++
			}
			toks = append(toks, token{tokNum, s[i:j]})
			i = j
		case c == '"' || c == '\'':
			j := i + 1
			for j < len(s) {
				if s[j] == '\\' && j+1 < len(s) {
					j += 2
					continue
				}
				if s[j] == c {
					break
				}
				j++
			}
			if j >= len(s) {
				return nil, false
			}
			toks = append(toks, token{tokStr, s[i : j+1]})
			i = j + 1
		case c == '$':
			j := i + 1
			if j >= len(s) || !isIdentStart(s[j]) {
				return nil, false
			}
			for j < len(s) && isIdentChar(s[j]) {
				j++
			}
			base := s[i+1 : j]
			chain := ""
			for j+1 < len(s) && s[j] == '.' && isIdentStart(s[j+1]) {
				k := j + 1
				for k < len(s) && isIdentChar(s[k]) {
					k++
				}
				chain += s[j:k]
				j = k
			}
			if sc.bound(base) {
				toks = append(toks, token{tokVar, "$" + base + chain})
			} else {
				toks = append(toks, token{tokField, "." + base + chain})
			}
			i = j
		case c == '.' && i+1 < len(s) && isIdentStart(s[i+1]):
			j := i + 1
			for j < len(s) && isIdentChar(s[j]) {
				j++
			}
			field := s[i:j]
			for j+1 < len(s) && s[j] == '.' && isIdentStart(s[j+1]) {
				k := j + 1
				for k < len(s) && isIdentChar(s[k]) {
					k++
				}
				field += s[j:k]
				j = k
			}
			toks = append(toks, token{tokField, field})
			i = j
		case isIdentStart(c):
			j := i
			for j < len(s) && isIdentChar(s[j]) {
				j++
			}
			word := s[i:j]
			switch word {
			case "and", "or", "not":
				toks = append(toks, token{tokOp, word})
			default:
				toks = append(toks, token{tokIdent, word})
			}
			i = j
		case strings.HasPrefix(s[i:], "&&"):
			toks = append(toks, token{tokOp, "&&"})
			i += 2
		case strings.HasPrefix(s[i:], "||"):
			toks = append(toks, token{tokOp, "||"})
			i += 2
		case strings.HasPrefix(s[i:], "=="):
			toks = append(toks, token{tokOp, "=="})
			i += 2
		case strings.HasPrefix(s[i:], "!="):
			toks = append(toks, token{tokOp, "!="})
			i += 2
		case strings.HasPrefix(s[i:], "<="):
			toks = append(toks, token{tokOp, "<="})
			i += 2
		case strings.HasPrefix(s[i:], ">="):
			toks = append(toks, token{tokOp, ">="})
			i += 2
		case strings.ContainsRune("<>!+-*/(),|{}:", rune(c)):
			toks = append(toks, token{tokOp, string(c)})
			i++
		default:
			return nil, false
		}
	}
	toks = append(toks, token{tokEOF, ""})
	return toks, true
}

// ---------------------------------------------------------------------------
// Parser dengan pendakatan presedensi (precedence climbing)

type exprRes struct {
	out    string
	plain  bool // true = atom (tidak perlu dibungkus kurung saat jadi argumen)
	failed bool
}

type parser struct {
	toks []token
	pos  int
	sc   *scope
}

func (p *parser) peek() token  { return p.toks[p.pos] }
func (p *parser) next() token  { t := p.toks[p.pos]; p.pos++; return t }
func (p *parser) isOp(ops ...string) bool {
	t := p.peek()
	if t.kind != tokOp {
		return false
	}
	for _, o := range ops {
		if t.val == o {
			return true
		}
	}
	return false
}

// arg mempersiapkan hasil sebagai argumen fungsi: komposit dibungkus ().
func (r exprRes) arg() string {
	if r.plain {
		return r.out
	}
	return "(" + r.out + ")"
}

var binOps = map[string]string{
	"||": "or", "&&": "and",
	"==": "eq", "!=": "ne", "<": "lt", ">": "gt", "<=": "le", ">=": "ge",
	"+": "add", "-": "sub", "*": "mul", "/": "div",
}

var opPrec = map[string]int{
	"||": 1,
	"&&": 2,
	"==": 3, "!=": 3, "<": 3, ">": 3, "<=": 3, ">=": 3,
	"+": 4, "-": 4,
	"*": 5, "/": 5,
}

func (p *parser) binary(minPrec int) exprRes {
	left := p.unary()
	for !left.failed && p.peek().kind == tokOp {
		op := p.peek().val
		prec, ok := opPrec[op]
		if !ok || prec < minPrec {
			break
		}
		p.next()
		right := p.binary(prec + 1)
		if right.failed {
			return exprRes{failed: true}
		}
		left = exprRes{
			out:   binOps[op] + " " + left.arg() + " " + right.arg(),
			plain: false,
		}
	}
	return left
}

func (p *parser) unary() exprRes {
	if p.isOp("!", "not") {
		op := p.next()
		v := p.unary()
		if v.failed {
			return v
		}
		word := "not"
		if op.val == "!" {
			word = "not"
		}
		return exprRes{out: word + " " + v.arg(), plain: false}
	}
	return p.primary()
}

func (p *parser) primary() exprRes {
	t := p.peek()
	switch t.kind {
	case tokNum, tokStr, tokField, tokVar:
		p.next()
		return exprRes{out: t.val, plain: true}
	case tokIdent:
		p.next()
		switch t.val {
		case "true", "false":
			return exprRes{out: t.val, plain: true}
		case "null", "nil":
			return exprRes{out: "nil", plain: true}
		}
		// Panggilan fungsi: f(a, b) → f a b
		if p.isOp("(") {
			p.next()
			args := ""
			if p.isOp(")") {
				p.next()
			} else {
				for {
					a := p.pipe()
					if a.failed {
						return a
					}
					if args != "" {
						args += " "
					}
					args += a.arg()
					if p.isOp(",") {
						p.next()
						continue
					}
					if p.isOp(")") {
						p.next()
						break
					}
					return exprRes{failed: true}
				}
			}
			if args == "" {
				return exprRes{out: t.val, plain: true}
			}
			return exprRes{out: t.val + " " + args, plain: false}
		}
		return exprRes{out: t.val, plain: true}
	case tokOp:
		if t.val == "(" {
			p.next()
			inner := p.pipe()
			if inner.failed {
				return inner
			}
			if !p.isOp(")") {
				return exprRes{failed: true}
			}
			p.next()
			return exprRes{out: "(" + inner.out + ")", plain: false}
		}
		// Literal object: {kunci: nilai, ...} → dict "kunci" nilai ...
		// (template Go tidak punya literal object).
		if t.val == "{" {
			p.next()
			out := "dict"
			if p.isOp("}") {
				p.next()
				return exprRes{out: out, plain: true}
			}
			for {
				kt := p.peek()
				var key string
				switch kt.kind {
				case tokIdent:
					key = `"` + kt.val + `"`
				case tokStr:
					key = kt.val
				default:
					return exprRes{failed: true}
				}
				p.next()
				if !p.isOp(":") {
					return exprRes{failed: true}
				}
				p.next()
				v := p.pipe()
				if v.failed {
					return v
				}
				out += " " + key + " " + v.arg()
				if p.isOp(",") {
					p.next()
					continue
				}
				if p.isOp("}") {
					p.next()
					return exprRes{out: out, plain: false}
				}
				return exprRes{failed: true}
			}
		}
	}
	return exprRes{failed: true}
}

// pipe menangani pipeline: ekspresi | fungsi(args) | ...
func (p *parser) pipe() exprRes {
	out := p.binary(1)
	if out.failed {
		return out
	}
	for p.isOp("|") {
		p.next()
		if p.peek().kind != tokIdent {
			return exprRes{failed: true}
		}
		name := p.next()
		seg := name.val
		if p.isOp("(") {
			p.next()
			args := ""
			if p.isOp(")") {
				p.next()
			} else {
				for {
					a := p.binary(1)
					if a.failed {
						return a
					}
					if args != "" {
						args += " "
					}
					args += a.arg()
					if p.isOp(",") {
						p.next()
						continue
					}
					if p.isOp(")") {
						p.next()
						break
					}
					return exprRes{failed: true}
				}
			}
			if args != "" {
				seg += " " + args
			}
		}
		out = exprRes{out: out.arg() + " | " + seg, plain: false}
	}
	return out
}

// translateExpr menerjemahkan ekspresi Blade menjadi aksi template Go.
// Bila terjemahan gagal, fallback ke konversi variabel saja.
func translateExpr(s string, sc *scope) string {
	toks, ok := lexExpr(s, sc)
	if !ok {
		return fallbackVars(s, sc)
	}
	p := &parser{toks: toks, sc: sc}
	res := p.pipe()
	if res.failed || p.peek().kind != tokEOF {
		return fallbackVars(s, sc)
	}
	return res.out
}

// fallbackVars melakukan konversi minimum: $x → .x (di luar string) dan
// penggantian && || ! yang tidak bisa ditangani template Go.
func fallbackVars(s string, sc *scope) string {
	var sb strings.Builder
	i := 0
	isIdentStart := func(c byte) bool {
		return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
	}
	isIdentChar := func(c byte) bool {
		return isIdentStart(c) || (c >= '0' && c <= '9')
	}
	for i < len(s) {
		c := s[i]
		switch {
		case c == '"' || c == '\'':
			j := i + 1
			for j < len(s) {
				if s[j] == '\\' && j+1 < len(s) {
					j += 2
					continue
				}
				if s[j] == c {
					break
				}
				j++
			}
			if j >= len(s) {
				sb.WriteString(s[i:])
				return sb.String()
			}
			sb.WriteString(s[i : j+1])
			i = j + 1
		case c == '$' && i+1 < len(s) && isIdentStart(s[i+1]):
			j := i + 1
			for j < len(s) && isIdentChar(s[j]) {
				j++
			}
			name := s[i+1 : j]
			if sc.bound(name) {
				sb.WriteString("$" + name)
			} else {
				sb.WriteString("." + name)
			}
			i = j
		case strings.HasPrefix(s[i:], "&&"):
			sb.WriteString(" and ")
			i += 2
		case strings.HasPrefix(s[i:], "||"):
			sb.WriteString(" or ")
			i += 2
		case c == '!' && (i+1 >= len(s) || s[i+1] != '='):
			sb.WriteString("not ")
			i++
		default:
			sb.WriteByte(c)
			i++
		}
	}
	return sb.String()
}

// ---------------------------------------------------------------------------
// Scanner konversi file template

var directiveNames = map[string]bool{
	"extends": true, "include": true, "section": true, "yield": true,
	"if": true, "elseif": true, "else": true, "endif": true,
	"foreach": true, "endforeach": true,
	"isset": true, "endisset": true,
	"csrf": true, "method": true, "json": true, "php": true,
	"endsection": true,
}

// convert mengubah sumber Blade menjadi sintaks template Go.
func convert(src string, sc *scope) string {
	var out strings.Builder
	i := 0
	for i < len(src) {
		switch {
		case strings.HasPrefix(src[i:], "{{--"):
			// Komentar Blade {{-- ... --}}
			if j := strings.Index(src[i+4:], "--}}"); j >= 0 {
				i += 4 + j + 4
			} else {
				i = len(src)
			}
		case strings.HasPrefix(src[i:], "{!!"):
			// Output mentah: {!! x !!} → {{ x | raw }}
			if j := strings.Index(src[i+3:], "!!}"); j >= 0 {
				inner := translateExpr(src[i+3:i+3+j], sc)
				out.WriteString("{{ " + inner + " | raw }}")
				i += 3 + j + 3
			} else {
				out.WriteByte(c0(src, i))
				i++
			}
		case strings.HasPrefix(src[i:], "{{"):
			// Aksi: {{ x }} → {{ <terjemahan> }}
			if j := strings.Index(src[i+2:], "}}"); j >= 0 {
				inner := translateExpr(src[i+2:i+2+j], sc)
				out.WriteString("{{" + inner + "}}")
				i += 2 + j + 2
			} else {
				out.WriteByte(c0(src, i))
				i++
			}
		case src[i] == '@':
			// convertDirective mengembalikan indeks absolut setelah direktif
			// (0 = bukan direktif).
			if n := convertDirective(src, i, sc, &out); n > i {
				i = n
			} else {
				out.WriteByte('@')
				i++
			}
		default:
			// Teks biasa disalin apa adanya — $var HANYA dikonversi di
			// dalam aksi {{ }} / direktif, sama seperti Blade asli.
			out.WriteByte(src[i])
			i++
		}
	}
	return out.String()
}

func c0(s string, i int) byte { return s[i] }

// convertDirective mengonversi satu direktif Blade di posisi i.
// Mengembalikan jumlah byte yang dikonsumsi, atau 0 bila bukan direktif.
func convertDirective(src string, i int, sc *scope, out *strings.Builder) int {
	j := i + 1
	isIdentChar := func(c byte) bool {
		return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
	}
	if j >= len(src) || !(src[j] == '_' || (src[j] >= 'a' && src[j] <= 'z') || (src[j] >= 'A' && src[j] <= 'Z')) {
		return 0
	}
	start := j
	for j < len(src) && isIdentChar(src[j]) {
		j++
	}
	name := src[start:j]
	if !directiveNames[name] {
		return 0
	}
	rest := src[j:]

	skipWS := func(s string) string { return strings.TrimLeft(s, " \t\r\n") }

	// Kurung perlu diimbangi dengan memperhatikan string bersarang.
	needParen := func(s string) (inner string, end int, ok bool) {
		s2 := skipWS(s)
		if s2 == "" || s2[0] != '(' {
			return "", 0, false
		}
		inner, after, ok := matchParen(s, len(s)-len(s2))
		if !ok {
			return "", 0, false
		}
		return inner, after, true
	}

	switch name {
	case "extends":
		inner, end, ok := needParen(rest)
		if !ok {
			return 0
		}
		target := skipWS(inner)
		if len(target) < 2 || (target[0] != '"' && target[0] != '\'') {
			return 0
		}
		ref := target[1 : len(target)-1]
		out.WriteString(`{{template "` + ref + `" .}}`)
		return j + end

	case "include":
		inner, end, ok := needParen(rest)
		if !ok {
			return 0
		}
		ref, after, ok := readQuoted(inner, len(inner)-len(skipWS(inner)))
		if !ok {
			return 0
		}
		tail := skipWS(inner[after:])
		if strings.HasPrefix(tail, ",") {
			expr := translateExpr(strings.TrimPrefix(tail, ","), sc)
			out.WriteString(`{{template "` + ref + `" ` + expr + `}}`)
		} else {
			out.WriteString(`{{template "` + ref + `" .}}`)
		}
		return j + end

	case "section":
		inner, end, ok := needParen(rest)
		if !ok {
			return 0
		}
		secName, after, ok := readQuoted(inner, len(inner)-len(skipWS(inner)))
		if !ok {
			return 0
		}
		tail := skipWS(inner[after:])
		out.WriteString(`{{define "` + secName + `"}}`)
		if strings.HasPrefix(tail, ",") {
			out.WriteString(`{{` + translateExpr(strings.TrimPrefix(tail, ","), sc) + `}}`)
			out.WriteString(`{{end}}`)
		}
		return j + end

	case "yield":
		// Default (bila ada) sudah didaftarkan terpisah oleh Engine.
		_, end, ok := needParen(rest)
		if !ok {
			return 0
		}
		inner, _, _ := needParen(rest)
		sName, _, ok := readQuoted(inner, len(inner)-len(skipWS(inner)))
		if !ok {
			return 0
		}
		out.WriteString(`{{template "` + sName + `" .}}`)
		return j + end

	case "if", "elseif", "isset":
		inner, end, ok := needParen(rest)
		if !ok {
			return 0
		}
		expr := translateExpr(inner, sc)
		switch name {
		case "if", "isset":
			out.WriteString(`{{if ` + expr + `}}`)
		default:
			out.WriteString(`{{else if ` + expr + `}}`)
		}
		return j + end

	case "else":
		out.WriteString(`{{else}}`)
		return j

	case "endif", "endsection", "endisset":
		out.WriteString(`{{end}}`)
		return j

	case "foreach":
		inner, end, ok := needParen(rest)
		if !ok {
			return 0
		}
		pos := findTopLevel(inner, " as ")
		if pos < 0 {
			return 0
		}
		lhs := strings.TrimSpace(inner[:pos])
		rhs := strings.TrimSpace(inner[pos+4:])
		// Konversi sumber perulangan di scope luar, lalu ikat variabelnya.
		rangeSrc := translateExpr(lhs, sc)
		var names []string
		if kpos := findTopLevel(rhs, "=>"); kpos >= 0 {
			k := strings.TrimSpace(rhs[:kpos])
			v := strings.TrimSpace(rhs[kpos+2:])
			kv, ok1 := varName(k)
			vv, ok2 := varName(v)
			if !ok1 || !ok2 {
				return 0
			}
			names = []string{kv, vv}
			sc.push(names...)
			out.WriteString(`{{range $` + kv + `, $` + vv + ` := ` + rangeSrc + `}}`)
		} else {
			vv, ok := varName(rhs)
			if !ok {
				return 0
			}
			names = []string{vv}
			sc.push(names...)
			out.WriteString(`{{range $` + vv + ` := ` + rangeSrc + `}}`)
		}
		return j + end

	case "endforeach":
		sc.pop()
		out.WriteString(`{{end}}`)
		return j

	case "csrf":
		out.WriteString(`<input type="hidden" name="_token" value="{{.csrf_token}}">`)
		return j

	case "method":
		inner, end, ok := needParen(rest)
		if !ok {
			return 0
		}
		m := skipWS(inner)
		if len(m) < 2 {
			return 0
		}
		out.WriteString(`<input type="hidden" name="_method" value="` + m[1:len(m)-1] + `">`)
		return j + end

	case "json":
		inner, end, ok := needParen(rest)
		if !ok {
			return 0
		}
		out.WriteString(`{{` + translateExpr(inner, sc) + ` | tojson}}`)
		return j + end

	case "php":
		// Blok @php ... @endphp dibuang (tidak didukung di template Go).
		if k := strings.Index(rest, "@endphp"); k >= 0 {
			return j + k + len("@endphp")
		}
		return 0
	}
	return 0
}

// varName mengambil nama variabel "$x" (boleh tanpa $).
func varName(s string) (string, bool) {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "$")
	if s == "" {
		return "", false
	}
	c := s[0]
	if c != '_' && !(c >= 'a' && c <= 'z') && !(c >= 'A' && c <= 'Z') {
		return "", false
	}
	return s, true
}

// matchParen mencari pasangan kurung tutup dari posisi open (src[open]=='('),
// memperhatikan string bersarang. Mengembalikan isi dalam kurung dan indeks
// setelah kurung tutup.
func matchParen(s string, open int) (inner string, after int, ok bool) {
	depth := 0
	var quote byte
	for i := open; i < len(s); i++ {
		c := s[i]
		if quote != 0 {
			if c == '\\' {
				i++
				continue
			}
			if c == quote {
				quote = 0
			}
			continue
		}
		switch c {
		case '"', '\'':
			quote = c
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return s[open+1 : i], i + 1, true
			}
		}
	}
	return "", 0, false
}

// readQuoted membaca string literal mulai dari posisi open (kutip).
func readQuoted(s string, open int) (val string, after int, ok bool) {
	if open >= len(s) || (s[open] != '"' && s[open] != '\'') {
		return "", 0, false
	}
	q := s[open]
	for i := open + 1; i < len(s); i++ {
		if s[i] == '\\' {
			i++
			continue
		}
		if s[i] == q {
			return s[open+1 : i], i + 1, true
		}
	}
	return "", 0, false
}

// findTopLevel mencari substring di kedalaman kurung 0 (di luar string).
func findTopLevel(s, sub string) int {
	depth := 0
	var quote byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		if quote != 0 {
			if c == '\\' {
				i++
				continue
			}
			if c == quote {
				quote = 0
			}
			continue
		}
		switch c {
		case '"', '\'':
			quote = c
		case '(':
			depth++
		case ')':
			depth--
		default:
			if depth == 0 && strings.HasPrefix(s[i:], sub) {
				return i
			}
		}
	}
	return -1
}
