package interp

import (
	"math"
	"strings"

	"garurda/internal/domain"
)

// errUnsupportedJSON is returned when a value cannot be serialised to JSON.
var errUnsupportedJSON = errStr("value cannot be encoded to JSON")

type errStr string

func (e errStr) Error() string { return string(e) }

// valueMethod exposes methods on builtin value types, so that
// "a,b,c".split(",") works alongside strings.split("a,b,c", ",").
func valueMethod(recv domain.Value, name string) (func(in *Interp, args []domain.Value, pos domain.Position) (domain.Value, error), bool) {
	switch recv.(type) {
	case domain.Str:
		if m, ok := stringMethods[name]; ok {
			return m, true
		}
	case *domain.Arr:
		if m, ok := arrayMethods[name]; ok {
			return m, true
		}
	}
	return nil, false
}

func argStr(a domain.Value) string {
	if s, ok := a.(domain.Str); ok {
		return string(s)
	}
	return ""
}

// stringMethods are callable directly on string values.
var stringMethods = map[string]func(in *Interp, args []domain.Value, pos domain.Position) (domain.Value, error){
	"len": func(in *Interp, a []domain.Value, p domain.Position) (domain.Value, error) {
		return domain.Int(len([]rune(argStr(a[0])))), nil
	},
	"upper": func(in *Interp, a []domain.Value, p domain.Position) (domain.Value, error) {
		return domain.Str(strings.ToUpper(argStr(a[0]))), nil
	},
	"lower": func(in *Interp, a []domain.Value, p domain.Position) (domain.Value, error) {
		return domain.Str(strings.ToLower(argStr(a[0]))), nil
	},
	"trim": func(in *Interp, a []domain.Value, p domain.Position) (domain.Value, error) {
		return domain.Str(strings.TrimSpace(argStr(a[0]))), nil
	},
	"split": func(in *Interp, a []domain.Value, p domain.Position) (domain.Value, error) {
		out := &domain.Arr{}
		for _, part := range strings.Split(argStr(a[0]), argStr(a[1])) {
			out.Append(domain.Str(part))
		}
		return out, nil
	},
	"contains": func(in *Interp, a []domain.Value, p domain.Position) (domain.Value, error) {
		return domain.Bool(strings.Contains(argStr(a[0]), a[1].String())), nil
	},
	"starts_with": func(in *Interp, a []domain.Value, p domain.Position) (domain.Value, error) {
		return domain.Bool(strings.HasPrefix(argStr(a[0]), argStr(a[1]))), nil
	},
	"ends_with": func(in *Interp, a []domain.Value, p domain.Position) (domain.Value, error) {
		return domain.Bool(strings.HasSuffix(argStr(a[0]), argStr(a[1]))), nil
	},
	"replace": func(in *Interp, a []domain.Value, p domain.Position) (domain.Value, error) {
		return domain.Str(strings.ReplaceAll(argStr(a[0]), argStr(a[1]), a[2].String())), nil
	},
	"slice": func(in *Interp, a []domain.Value, p domain.Position) (domain.Value, error) {
		r := []rune(argStr(a[0]))
		lo, hi, err := sliceBounds(in, a[1:], len(r), p)
		if err != nil {
			return nil, err
		}
		return domain.Str(string(r[lo:hi])), nil
	},
	"repeat": func(in *Interp, a []domain.Value, p domain.Position) (domain.Value, error) {
		n, _ := domain.AsInt(a[1])
		if n < 0 {
			n = 0
		}
		return domain.Str(strings.Repeat(argStr(a[0]), int(n))), nil
	},
	"escape_html": func(in *Interp, a []domain.Value, p domain.Position) (domain.Value, error) {
		return domain.Str(domain.EscapeHTML(argStr(a[0]))), nil
	},
}

func arrLen(v domain.Value) int {
	if a, ok := v.(*domain.Arr); ok {
		return len(a.Items)
	}
	return 0
}

// arrayMethods are callable directly on array values. It is populated in init
// to avoid a package initialisation cycle: the methods reference interpreter
// helpers, which reference the method tables.
var arrayMethods map[string]func(in *Interp, args []domain.Value, pos domain.Position) (domain.Value, error)

func init() {
	arrayMethods = map[string]func(in *Interp, args []domain.Value, pos domain.Position) (domain.Value, error){
		"len": func(in *Interp, a []domain.Value, p domain.Position) (domain.Value, error) {
			return domain.Int(arrLen(a[0])), nil
		},
		"first": func(in *Interp, a []domain.Value, p domain.Position) (domain.Value, error) {
			arr := a[0].(*domain.Arr)
			if len(arr.Items) == 0 {
				return domain.Null{}, nil
			}
			return arr.Items[0], nil
		},
		"last": func(in *Interp, a []domain.Value, p domain.Position) (domain.Value, error) {
			arr := a[0].(*domain.Arr)
			if len(arr.Items) == 0 {
				return domain.Null{}, nil
			}
			return arr.Items[len(arr.Items)-1], nil
		},
		"push": func(in *Interp, a []domain.Value, p domain.Position) (domain.Value, error) {
			arr := a[0].(*domain.Arr)
			out := arr.Copy()
			for _, extra := range a[1:] {
				if out.Elem != nil {
					if err := in.checkType(out.Elem, extra, p); err != nil {
						return nil, err
					}
				}
				out.Append(extra)
			}
			return out, nil
		},
		"pop": func(in *Interp, a []domain.Value, p domain.Position) (domain.Value, error) {
			arr := a[0].(*domain.Arr)
			if len(arr.Items) == 0 {
				return domain.Null{}, nil
			}
			return arr.Copy().Pop(), nil
		},
		"join": func(in *Interp, a []domain.Value, p domain.Position) (domain.Value, error) {
			arr := a[0].(*domain.Arr)
			sep := ""
			if len(a) > 1 {
				sep = a[1].String()
			}
			parts := make([]string, 0, len(arr.Items))
			for _, it := range arr.Items {
				parts = append(parts, it.String())
			}
			return domain.Str(strings.Join(parts, sep)), nil
		},
		"includes": func(in *Interp, a []domain.Value, p domain.Position) (domain.Value, error) {
			arr := a[0].(*domain.Arr)
			for _, it := range arr.Items {
				if valuesEqual(it, a[1]) {
					return domain.Bool(true), nil
				}
			}
			return domain.Bool(false), nil
		},
		"slice": func(in *Interp, a []domain.Value, p domain.Position) (domain.Value, error) {
			arr := a[0].(*domain.Arr)
			lo, hi, err := sliceBounds(in, a[1:], len(arr.Items), p)
			if err != nil {
				return nil, err
			}
			out := make([]domain.Value, hi-lo)
			copy(out, arr.Items[lo:hi])
			return &domain.Arr{Items: out, Elem: arr.Elem}, nil
		},
		"reverse": func(in *Interp, a []domain.Value, p domain.Position) (domain.Value, error) {
			arr := a[0].(*domain.Arr)
			out := &domain.Arr{Items: make([]domain.Value, len(arr.Items)), Elem: arr.Elem}
			for i, it := range arr.Items {
				out.Items[len(arr.Items)-1-i] = it
			}
			return out, nil
		},
		"map": func(in *Interp, a []domain.Value, p domain.Position) (domain.Value, error) {
			arr := a[0].(*domain.Arr)
			out := &domain.Arr{}
			for i, it := range arr.Items {
				res, err := in.callFnHelper(a[1], []domain.Value{it, domain.Int(i)}, p)
				if err != nil {
					return nil, err
				}
				out.Append(res)
			}
			return out, nil
		},
		"filter": func(in *Interp, a []domain.Value, p domain.Position) (domain.Value, error) {
			arr := a[0].(*domain.Arr)
			out := &domain.Arr{Elem: arr.Elem}
			for i, it := range arr.Items {
				keep, err := in.callFnHelper(a[1], []domain.Value{it, domain.Int(i)}, p)
				if err != nil {
					return nil, err
				}
				if domain.Truthy(keep) {
					out.Append(it)
				}
			}
			return out, nil
		},
		"indices": func(in *Interp, a []domain.Value, p domain.Position) (domain.Value, error) {
			arr := a[0].(*domain.Arr)
			out := &domain.Arr{}
			for i := range arr.Items {
				out.Append(domain.Int(i))
			}
			return out, nil
		},
	}
}

// builtinMethod handles methods on builtin objects that carry state, such as
// error values.
func builtinMethod(recv domain.Value, name string) (func(in *Interp, args []domain.Value, pos domain.Position) (domain.Value, error), bool) {
	ev, ok := recv.(*domain.ErrorValue)
	if !ok {
		return nil, false
	}
	if name == "is" {
		return func(in *Interp, a []domain.Value, p domain.Position) (domain.Value, error) {
			return domain.Bool(ev.Code == a[0].String()), nil
		}, true
	}
	return nil, false
}

// Math wrappers keep the hot paths free of repeated package qualifiers.
func absFloat(f float64) float64    { return math.Abs(f) }
func floorFloat(f float64) float64  { return math.Floor(f) }
func ceilFloat(f float64) float64   { return math.Ceil(f) }
func roundFloat(f float64) float64  { return math.Round(f) }
func powFloat(b, e float64) float64 { return math.Pow(b, e) }
func sqrtFloat(f float64) float64   { return math.Sqrt(f) }
func piValue() float64              { return math.Pi }
