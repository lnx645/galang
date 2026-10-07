package domain

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

// TypeTag identifies the runtime type of a Value.
type TypeTag string

const (
	TypeNull       TypeTag = "null"
	TypeBool       TypeTag = "bool"
	TypeInt        TypeTag = "int"
	TypeFloat      TypeTag = "float"
	TypeString     TypeTag = "string"
	TypeArray      TypeTag = "array"
	TypeObject     TypeTag = "object"
	TypeFunc       TypeTag = "function"
	TypeError      TypeTag = "error"
	TypeCallObject TypeTag = "callable-object" // request, response, session, ...
)

// Value is a Garurda runtime value.
type Value interface {
	Type() TypeTag
	// String renders the value for print.
	String() string
	// Truthy is the boolean interpretation of the value.
	Truthy() bool
}

// ---- null ----

// Null is the single null value.
type Null struct{}

func (Null) Type() TypeTag  { return TypeNull }
func (Null) String() string { return "null" }
func (Null) Truthy() bool   { return false }

// ---- bool ----

// Bool is a boolean value.
type Bool bool

func (Bool) Type() TypeTag { return TypeBool }
func (b Bool) String() string {
	if b {
		return "true"
	}
	return "false"
}
func (b Bool) Truthy() bool { return bool(b) }

// ---- numbers ----

// Int is a 64-bit integer.
type Int int64

func (Int) Type() TypeTag { return TypeInt }
func (i Int) String() string {
	return strconv.FormatInt(int64(i), 10)
}
func (i Int) Truthy() bool { return i != 0 }

// Float is a 64-bit float.
type Float float64

func (Float) Type() TypeTag { return TypeFloat }

// String prints whole floats without a trailing ".0".
func (f Float) String() string {
	v := float64(f)
	if v == math.Trunc(v) && math.Abs(v) < 1e15 {
		return strconv.FormatInt(int64(v), 10)
	}
	return strconv.FormatFloat(v, 'g', -1, 64)
}
func (f Float) Truthy() bool { return f != 0 }

// smallInts caches small integers so that arithmetic on literals does not
// allocate. Boxing every 1..255 would otherwise churn the garbage collector.
var smallInts = func() []Value {
	const lo, hi = -128, 512
	tbl := make([]Value, hi-lo+1)
	for i := range tbl {
		tbl[i] = Int(i + lo)
	}
	return tbl
}()

// CacheInt returns a shared Value for small integers.
func CacheInt(i int64) (Value, bool) {
	if i >= -128 && i <= 512 {
		return smallInts[i+128], true
	}
	return nil, false
}

// ---- string ----

// Str is a UTF-8 string.
type Str string

func (Str) Type() TypeTag    { return TypeString }
func (s Str) String() string { return string(s) }
func (s Str) Truthy() bool   { return len(s) > 0 }

// ---- array ----

// Arr is a mutable list of values with value semantics: assignment copies
// lazily (copy-on-write) through the shared backing store.
type Arr struct {
	Items []Value
	// Elem is the declared element type (array<int>), nil when dynamic.
	Elem *TypeExpr
	// shared is non-nil when Items aliases another array; the first mutation
	// clones. Track mutation explicitly rather than comparing slice headers.
	shared bool
}

func (*Arr) Type() TypeTag { return TypeArray }

func (a *Arr) String() string {
	if a == nil || len(a.Items) == 0 {
		return "[]"
	}
	parts := make([]string, len(a.Items))
	for i, it := range a.Items {
		parts[i] = it.String()
	}
	return "[" + strings.Join(parts, ", ") + "]"
}
func (a *Arr) Truthy() bool { return a != nil && len(a.Items) > 0 }

// NewArr builds an array from values.
func NewArr(items ...Value) *Arr { return &Arr{Items: items} }

// Own prepares the array for mutation, cloning a shared backing store.
func (a *Arr) Own() {
	if a.shared {
		cp := make([]Value, len(a.Items))
		copy(cp, a.Items)
		a.Items = cp
		a.shared = false
	}
}

// Append adds a value at the end, respecting copy-on-write.
func (a *Arr) Append(v Value) {
	a.Own()
	a.Items = append(a.Items, v)
}

// Set replaces the element at i.
func (a *Arr) Set(i int, v Value) {
	a.Own()
	a.Items[i] = v
}

// Pop removes and returns the last element.
func (a *Arr) Pop() Value {
	a.Own()
	last := len(a.Items) - 1
	v := a.Items[last]
	a.Items = a.Items[:last]
	return v
}

// Copy returns an array that is safe to mutate independently of a.
func (a *Arr) Copy() *Arr {
	if a == nil {
		return NewArr()
	}
	cp := make([]Value, len(a.Items))
	copy(cp, a.Items)
	return &Arr{Items: cp, Elem: a.Elem}
}

// ---- object ----

// Obj is a string-keyed map that remembers insertion order. It is used both
// for object literals and for built-in objects (modules, request, response).
type Obj struct {
	keys   []string
	fields map[string]Value
	// tag lets builtin objects report a friendlier type name (request, ...).
	tag TypeTag
}

// NewObj creates an empty object.
func NewObj() *Obj { return &Obj{fields: map[string]Value{}} }

// NewTaggedObj creates a builtin object that reports the given type name.
func NewTaggedObj(tag TypeTag) *Obj {
	return &Obj{fields: map[string]Value{}, tag: tag}
}

// Get returns a field and whether it exists.
func (o *Obj) Get(k string) (Value, bool) {
	if o == nil {
		return nil, false
	}
	v, ok := o.fields[k]
	return v, ok
}

// MustGet returns a field or an empty string.
func (o *Obj) MustGet(k string) Value {
	v, _ := o.Get(k)
	if v == nil {
		return Str("")
	}
	return v
}

// Set inserts or updates a field.
func (o *Obj) Set(k string, v Value) {
	if o.fields == nil {
		o.fields = map[string]Value{}
	}
	if _, ok := o.fields[k]; !ok {
		o.keys = append(o.keys, k)
	}
	o.fields[k] = v
}

// Delete removes a field, reporting whether it existed.
func (o *Obj) Delete(k string) bool {
	if o == nil {
		return false
	}
	if _, ok := o.fields[k]; !ok {
		return false
	}
	delete(o.fields, k)
	for i, k2 := range o.keys {
		if k2 == k {
			o.keys = append(o.keys[:i], o.keys[i+1:]...)
			break
		}
	}
	return true
}

// Keys returns the field names in insertion order.
func (o *Obj) Keys() []string {
	if o == nil {
		return nil
	}
	out := make([]string, len(o.keys))
	copy(out, o.keys)
	return out
}

// SortedKeys returns field names sorted alphabetically.
func (o *Obj) SortedKeys() []string {
	k := o.Keys()
	sort.Strings(k)
	return k
}

// Fields exposes the raw map, used when serialising to JSON.
func (o *Obj) Fields() map[string]Value { return o.fields }

func (o *Obj) Type() TypeTag {
	if o.tag != "" {
		return o.tag
	}
	return TypeObject
}

func (o *Obj) String() string {
	if o == nil {
		return "null"
	}
	parts := make([]string, 0, len(o.keys))
	for _, k := range o.keys {
		parts = append(parts, k+": "+o.fields[k].String())
	}
	return "{" + strings.Join(parts, ", ") + "}"
}
func (o *Obj) Truthy() bool { return o != nil && len(o.keys) > 0 }

// ---- builtins ----

// Builtin is a function implemented in Go and exposed to programs.
type Builtin struct {
	Name string
	Fn   func(args []Value, pos Position) (Value, error)
	// MinArgs is the number of required arguments.
	MinArgs int
	// VarArgs allows passing extra arguments beyond MinArgs.
	VarArgs bool
}

func (b *Builtin) Type() TypeTag { return TypeFunc }

// Arity reports the required and maximum accepted argument counts.
func (b *Builtin) Arity() (int, bool) { return b.MinArgs, b.VarArgs }
func (b *Builtin) String() string {
	return "<builtin " + b.Name + ">"
}
func (*Builtin) Truthy() bool { return true }

// ---- errors ----

// ErrorValue is a catchable error value.
type ErrorValue struct {
	Message string
	Code    string
	Status  int
	Stack   []string
}

func (*ErrorValue) Type() TypeTag { return TypeError }
func (e *ErrorValue) String() string {
	if e.Code != "" && e.Code != "error" {
		return e.Message + " (" + e.Code + ")"
	}
	return e.Message
}
func (*ErrorValue) Truthy() bool { return true }

// NewError builds an error value with the given message.
func NewError(msg string) *ErrorValue {
	return &ErrorValue{Message: msg, Code: "error", Status: 500}
}

// ---- helpers ----

// Truthy is the boolean interpretation of any value.
func Truthy(v Value) bool {
	if v == nil {
		return false
	}
	return v.Truthy()
}

// TypeName returns the runtime type name of a value.
func TypeName(v Value) string {
	if v == nil {
		return "null"
	}
	return string(v.Type())
}

// AsInt converts a value to an int when possible.
func AsInt(v Value) (int64, bool) {
	switch n := v.(type) {
	case Int:
		return int64(n), true
	case Float:
		return int64(n), true
	case Bool:
		if n {
			return 1, true
		}
		return 0, true
	}
	return 0, false
}

// AsFloat converts a value to a float when possible.
func AsFloat(v Value) (float64, bool) {
	switch n := v.(type) {
	case Int:
		return float64(n), true
	case Float:
		return float64(n), true
	case Bool:
		if n {
			return 1, true
		}
		return 0, true
	}
	return 0, false
}

// AsString renders a value for concatenation.
func AsString(v Value) string {
	if v == nil {
		return "null"
	}
	return v.String()
}

// Quote renders a string as a Garurda literal.
func Quote(s string) string { return strconv.Quote(s) }

// EscapeHTML escapes the five characters that matter for HTML text and
// attribute contexts.
func EscapeHTML(s string) string {
	var b strings.Builder
	b.Grow(len(s) + 16)
	for i := 0; i < len(s); {
		c := s[i]
		if c < utf8.RuneSelf {
			switch c {
			case '&':
				b.WriteString("&amp;")
			case '<':
				b.WriteString("&lt;")
			case '>':
				b.WriteString("&gt;")
			case '"':
				b.WriteString("&quot;")
			case '\'':
				b.WriteString("&#39;")
			default:
				b.WriteByte(c)
			}
			i++
			continue
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		b.WriteRune(r)
		i += size
	}
	return b.String()
}

// GoString is a debug helper.
func GoString(v Value) string { return fmt.Sprintf("%s(%s)", TypeName(v), v.String()) }
