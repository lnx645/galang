package interp

import (
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"garurda/internal/domain"
)

// ---- type checking ----

// checkType verifies that v satisfies the annotation t.
func (in *Interp) checkType(t *domain.TypeExpr, v domain.Value, p domain.Position) error {
	if t == nil || t.Name == "any" {
		return nil
	}
	if isNull(v) {
		if t.Nullable || t.Name == "null" {
			return nil
		}
		return in.typeError(t, v, p)
	}
	switch t.Name {
	case "int":
		if _, ok := v.(domain.Int); ok {
			return nil
		}
	case "float":
		switch v.(type) {
		case domain.Float:
			return nil
		case domain.Int:
			// int widens to float, as in Go.
			return nil
		}
	case "number":
		if _, ok := domain.AsFloat(v); ok {
			if _, isStr := v.(domain.Str); !isStr {
				if _, isBool := v.(domain.Bool); !isBool {
					return nil
				}
			}
		}
	case "string":
		if _, ok := v.(domain.Str); ok {
			return nil
		}
	case "bool":
		if _, ok := v.(domain.Bool); ok {
			return nil
		}
	case "array":
		a, ok := v.(*domain.Arr)
		if !ok {
			break
		}
		if len(t.Args) == 1 {
			// Defer to append-time checks: iterating here would make
			// `array<int> $x = [1, "a"]` an error instead of a clear
			// "cannot store string in array<int>".
			a.Elem = t.Args[0]
		}
		return nil
	case "object":
		if _, ok := v.(*domain.Obj); ok {
			return nil
		}
	case "function":
		switch v.(type) {
		case *Fn, *domain.Builtin:
			return nil
		}
	case "error":
		if _, ok := v.(*domain.ErrorValue); ok {
			return nil
		}
	case "request", "response", "session", "connection", "upload", "rows", "module", "mailer":
		if string(domain.TypeName(v)) == t.Name {
			return nil
		}
		if _, ok := v.(*domain.Obj); ok {
			return nil
		}
	default:
		// Unknown annotation names are treated as dynamic.
		return nil
	}
	return in.typeError(t, v, p)
}

func (in *Interp) typeError(t *domain.TypeExpr, v domain.Value, p domain.Position) error {
	return in.errf(p, "expected %s, got %s", t.String(), domain.TypeName(v))
}

func isNull(v domain.Value) bool {
	_, ok := v.(domain.Null)
	return ok || v == nil
}

// zeroValue produces the default value of a declared type.
func zeroValue(t *domain.TypeExpr) domain.Value {
	if t == nil {
		return domain.Null{}
	}
	switch t.Name {
	case "int":
		return domain.Int(0)
	case "float":
		return domain.Float(0)
	case "number":
		return domain.Int(0)
	case "string":
		return domain.Str("")
	case "bool":
		return domain.Bool(false)
	case "array":
		return &domain.Arr{Elem: elemOf(t)}
	case "object":
		return domain.NewObj()
	}
	return domain.Null{}
}

func elemOf(t *domain.TypeExpr) *domain.TypeExpr {
	if t == nil || len(t.Args) == 0 {
		return nil
	}
	return t.Args[0]
}

// ---- builtin modules ----

// newStringsModule builds the `strings` namespace.
func (in *Interp) newStringsModule() *domain.Obj {
	m := domain.NewObj()
	set := func(name string, min int, fn builtinFn) {
		m.Set(name, in.typeFn("strings."+name, min, fn))
	}
	str := func(in *Interp, args []domain.Value, pos domain.Position) (string, error) {
		s, ok := args[0].(domain.Str)
		if !ok {
			return "", in.errf(pos, "expects a string, got %s", domain.TypeName(args[0]))
		}
		return string(s), nil
	}

	set("upper", 1, func(in *Interp, a []domain.Value, p domain.Position) (domain.Value, error) {
		s, err := str(in, a, p)
		return domain.Str(strings.ToUpper(s)), err
	})
	set("lower", 1, func(in *Interp, a []domain.Value, p domain.Position) (domain.Value, error) {
		s, err := str(in, a, p)
		return domain.Str(strings.ToLower(s)), err
	})
	set("trim", 1, func(in *Interp, a []domain.Value, p domain.Position) (domain.Value, error) {
		s, err := str(in, a, p)
		return domain.Str(strings.TrimSpace(s)), err
	})
	set("split", 2, func(in *Interp, a []domain.Value, p domain.Position) (domain.Value, error) {
		s, err := str(in, a, p)
		if err != nil {
			return nil, err
		}
		sep, err := str(in, a[1:], p)
		if err != nil {
			return nil, err
		}
		out := &domain.Arr{}
		for _, part := range strings.Split(s, sep) {
			out.Append(domain.Str(part))
		}
		return out, nil
	})
	set("replace", 3, func(in *Interp, a []domain.Value, p domain.Position) (domain.Value, error) {
		s, err := str(in, a, p)
		if err != nil {
			return nil, err
		}
		old, err := str(in, a[1:], p)
		if err != nil {
			return nil, err
		}
		nw, err := str(in, a[2:], p)
		if err != nil {
			return nil, err
		}
		return domain.Str(strings.ReplaceAll(s, old, nw)), nil
	})
	set("starts_with", 2, func(in *Interp, a []domain.Value, p domain.Position) (domain.Value, error) {
		s, err := str(in, a, p)
		if err != nil {
			return nil, err
		}
		pre, err := str(in, a[1:], p)
		return domain.Bool(strings.HasPrefix(s, pre)), err
	})
	set("ends_with", 2, func(in *Interp, a []domain.Value, p domain.Position) (domain.Value, error) {
		s, err := str(in, a, p)
		if err != nil {
			return nil, err
		}
		suf, err := str(in, a[1:], p)
		return domain.Bool(strings.HasSuffix(s, suf)), err
	})
	set("index_of", 2, func(in *Interp, a []domain.Value, p domain.Position) (domain.Value, error) {
		s, err := str(in, a, p)
		if err != nil {
			return nil, err
		}
		sub, err := str(in, a[1:], p)
		if err != nil {
			return nil, err
		}
		return domain.Int(strings.Index(s, sub)), nil
	})
	set("pad_start", 2, func(in *Interp, a []domain.Value, p domain.Position) (domain.Value, error) {
		s, err := str(in, a, p)
		if err != nil {
			return nil, err
		}
		n, ok := domain.AsInt(a[1])
		if !ok {
			return nil, in.errf(p, "pad_start() expects a width")
		}
		pad := " "
		if len(a) > 2 {
			pad = a[2].String()
		}
		r := []rune(s)
		for len(r) < int(n) {
			r = append([]rune(pad), r...)
		}
		return domain.Str(string(r)), nil
	})
	set("repeat", 2, func(in *Interp, a []domain.Value, p domain.Position) (domain.Value, error) {
		s, err := str(in, a, p)
		if err != nil {
			return nil, err
		}
		n, _ := domain.AsInt(a[1])
		if n < 0 {
			n = 0
		}
		return domain.Str(strings.Repeat(s, int(n))), nil
	})
	set("char_at", 2, func(in *Interp, a []domain.Value, p domain.Position) (domain.Value, error) {
		s, err := str(in, a, p)
		if err != nil {
			return nil, err
		}
		r := []rune(s)
		i, _ := domain.AsInt(a[1])
		if int(i) < 0 || int(i) >= len(r) {
			return domain.Str(""), nil
		}
		return domain.Str(string(r[i])), nil
	})
	set("escape_html", 1, func(in *Interp, a []domain.Value, p domain.Position) (domain.Value, error) {
		s, err := str(in, a, p)
		return domain.Str(domain.EscapeHTML(s)), err
	})
	return m
}

// newMathModule builds the `math` namespace.
func (in *Interp) newMathModule() *domain.Obj {
	m := domain.NewObj()
	set := func(name string, min int, fn builtinFn) {
		m.Set(name, in.typeFn("math."+name, min, fn))
	}
	set("floor", 1, func(in *Interp, a []domain.Value, p domain.Position) (domain.Value, error) {
		f, ok := domain.AsFloat(a[0])
		if !ok {
			return nil, in.errf(p, "math.floor() expects a number")
		}
		return domain.Int(int64(floorFloat(f))), nil
	})
	set("ceil", 1, func(in *Interp, a []domain.Value, p domain.Position) (domain.Value, error) {
		f, ok := domain.AsFloat(a[0])
		if !ok {
			return nil, in.errf(p, "math.ceil() expects a number")
		}
		return domain.Int(int64(ceilFloat(f))), nil
	})
	set("round", 1, func(in *Interp, a []domain.Value, p domain.Position) (domain.Value, error) {
		f, ok := domain.AsFloat(a[0])
		if !ok {
			return nil, in.errf(p, "math.round() expects a number")
		}
		return domain.Int(int64(roundFloat(f))), nil
	})
	set("pow", 2, func(in *Interp, a []domain.Value, p domain.Position) (domain.Value, error) {
		b, ok1 := domain.AsFloat(a[0])
		e, ok2 := domain.AsFloat(a[1])
		if !ok1 || !ok2 {
			return nil, in.errf(p, "math.pow() expects numbers")
		}
		return domain.Float(powFloat(b, e)), nil
	})
	set("sqrt", 1, func(in *Interp, a []domain.Value, p domain.Position) (domain.Value, error) {
		f, ok := domain.AsFloat(a[0])
		if !ok || f < 0 {
			return nil, in.errf(p, "math.sqrt() expects a non-negative number")
		}
		return domain.Float(sqrtFloat(f)), nil
	})
	// pi is a constant, not a function: math.pi must print as a number.
	m.Set("pi", domain.Float(piValue()))
	m.Set("e", domain.Float(2.718281828459045))
	set("abs", 1, func(in *Interp, a []domain.Value, p domain.Position) (domain.Value, error) {
		f, ok := domain.AsFloat(a[0])
		if !ok {
			return nil, in.errf(p, "math.abs() expects a number")
		}
		if _, isInt := a[0].(domain.Int); isInt {
			if f < 0 {
				return domain.Int(int64(-f)), nil
			}
			return domain.Int(int64(f)), nil
		}
		return domain.Float(absFloat(f)), nil
	})
	set("min", 1, func(in *Interp, a []domain.Value, p domain.Position) (domain.Value, error) {
		return minMax(in, a, p, true)
	})
	set("max", 1, func(in *Interp, a []domain.Value, p domain.Position) (domain.Value, error) {
		return minMax(in, a, p, false)
	})
	set("sum", 1, func(in *Interp, a []domain.Value, p domain.Position) (domain.Value, error) {
		return sumArray(in, a[0], p)
	})
	return m
}

// newTimeModule builds the `time` namespace.
func (in *Interp) newTimeModule() *domain.Obj {
	m := domain.NewObj()
	m.Set("now_ms", in.typeFn("time.now_ms", 0, func(in *Interp, a []domain.Value, p domain.Position) (domain.Value, error) {
		return domain.Int(nowMillis()), nil
	}))
	m.Set("now", in.typeFn("time.now", 0, func(in *Interp, a []domain.Value, p domain.Position) (domain.Value, error) {
		return domain.Str(time.Now().Format(time.RFC3339)), nil
	}))
	m.Set("format", in.typeFn("time.format", 2, func(in *Interp, a []domain.Value, p domain.Position) (domain.Value, error) {
		t, err := timeFromValue(in, a[0], p)
		if err != nil {
			return nil, err
		}
		layout, _ := a[1].(domain.Str)
		return domain.Str(t.Format(string(layout))), nil
	}))
	return m
}

func timeFromValue(in *Interp, v domain.Value, p domain.Position) (time.Time, error) {
	ms, ok := domain.AsInt(v)
	if !ok {
		return time.Time{}, in.errf(p, "expects a millisecond timestamp")
	}
	return time.UnixMilli(ms), nil
}

// ---- json bridge ----

// jsonEncode serialises a Garurda value to JSON.
func jsonEncode(v domain.Value) (string, error) {
	var b strings.Builder
	if err := writeJSON(&b, v); err != nil {
		return "", err
	}
	return b.String(), nil
}

func writeJSON(b *strings.Builder, v domain.Value) error {
	switch x := v.(type) {
	case domain.Null:
		b.WriteString("null")
	case domain.Bool:
		if x {
			b.WriteString("true")
		} else {
			b.WriteString("false")
		}
	case domain.Int:
		b.WriteString(x.String())
	case domain.Float:
		b.WriteString(x.String())
	case domain.Str:
		enc, err := json.Marshal(string(x))
		if err != nil {
			return err
		}
		b.Write(enc)
	case *domain.Arr:
		b.WriteByte('[')
		for i, it := range x.Items {
			if i > 0 {
				b.WriteByte(',')
			}
			if err := writeJSON(b, it); err != nil {
				return err
			}
		}
		b.WriteByte(']')
	case *domain.Obj:
		b.WriteByte('{')
		for i, k := range x.Keys() {
			if i > 0 {
				b.WriteByte(',')
			}
			enc, err := json.Marshal(k)
			if err != nil {
				return err
			}
			b.Write(enc)
			b.WriteByte(':')
			val, _ := x.Get(k)
			if err := writeJSON(b, val); err != nil {
				return err
			}
		}
		b.WriteByte('}')
	case *domain.ErrorValue:
		enc, err := json.Marshal(map[string]interface{}{
			"error": map[string]interface{}{"message": x.Message, "code": x.Code},
		})
		if err != nil {
			return err
		}
		b.Write(enc)
	case *Fn, *domain.Builtin:
		return errUnsupportedJSON
	default:
		return errUnsupportedJSON
	}
	return nil
}

// jsonDecode parses JSON into Garurda values.
func jsonDecode(s string) (domain.Value, error) {
	dec := json.NewDecoder(strings.NewReader(s))
	dec.UseNumber()
	var raw interface{}
	if err := dec.Decode(&raw); err != nil {
		return nil, err
	}
	return fromJSON(raw), nil
}

func fromJSON(raw interface{}) domain.Value {
	switch x := raw.(type) {
	case nil:
		return domain.Null{}
	case bool:
		return domain.Bool(x)
	case string:
		return domain.Str(x)
	case json.Number:
		if i, err := x.Int64(); err == nil {
			return domain.Int(i)
		}
		f, _ := x.Float64()
		return domain.Float(f)
	case []interface{}:
		out := &domain.Arr{Items: make([]domain.Value, 0, len(x))}
		for _, it := range x {
			out.Append(fromJSON(it))
		}
		return out
	case map[string]interface{}:
		out := domain.NewObj()
		// json decoding does not guarantee order; sort for determinism.
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sortStrings(keys)
		for _, k := range keys {
			out.Set(k, fromJSON(x[k]))
		}
		return out
	}
	return domain.Null{}
}

// ---- small helpers ----

func parseIntStr(s string) (int64, bool) {
	v, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	return v, err == nil
}

func parseFloatStr(s string) (float64, bool) {
	v, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	return v, err == nil
}

func parseIntBase(s string, base int) (int64, bool) {
	v, err := strconv.ParseInt(strings.TrimSpace(s), base, 64)
	return v, err == nil
}

func strconvItoa(i int) string { return strconv.Itoa(i) }

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

func nowMillis() int64 { return time.Now().UnixNano() / int64(time.Millisecond) }
