package domain

// Node is any node of the Garurda syntax tree.
type Node interface {
	Pos() Position
	node()
}

// Position is a 1-based source location.
type Position struct {
	Line int
	Col  int
}

// ---- types (annotations) ----

// TypeExpr is a type annotation such as `int`, `?string` or `array<int>`.
type TypeExpr struct {
	Name     string
	Nullable bool
	Args     []*TypeExpr // element types, e.g. array<int>
	P        Position
}

func (t *TypeExpr) Pos() Position { return t.P }

// String renders the annotation the way it is written in source.
func (t *TypeExpr) String() string {
	if t == nil {
		return "any"
	}
	out := t.Name
	if len(t.Args) > 0 {
		out += "<"
		for i, a := range t.Args {
			if i > 0 {
				out += ", "
			}
			out += a.String()
		}
		out += ">"
	}
	if t.Nullable {
		return "?" + out
	}
	return out
}

// ---- expressions ----

// Expr is an expression node.
type Expr interface {
	Node
	expr()
}

// Ident references a variable.
type Ident struct {
	Name string
	P    Position
}

// IntLit is an integer literal.
type IntLit struct {
	Value int64
	P     Position
}

// FloatLit is a floating point literal.
type FloatLit struct {
	Value float64
	P     Position
}

// StrLit is a string literal, possibly with ${} interpolation.
type StrLit struct {
	Parts []StrPart
	P     Position
}

// StrPart is a literal chunk or an interpolated expression of a StrLit.
type StrPart struct {
	Lit    string
	Expr   Expr
	IsExpr bool
}

// BoolLit is true or false.
type BoolLit struct {
	Value bool
	P     Position
}

// NullLit is the null literal.
type NullLit struct{ P Position }

// ListLit is an array literal: [1, 2] or a comprehension [x * 2 for x in xs].
type ListLit struct {
	Elems []Expr
	P     Position

	// Comprehension, non-nil when written as [expr for x in src if cond].
	CompElem Expr
	CompVar  string
	CompIdx  string // second loop variable, for objects
	CompSrc  Expr
	CompCond Expr
}

// Pair is one key/value entry of an object literal.
type Pair struct {
	Key   string
	Value Expr
}

// ObjectLit is an object literal: {nama: "ada", umur: 3}.
type ObjectLit struct {
	Pairs []Pair
	P     Position
}

// IndexExpr is indexing: xs[0], obj["k"].
type IndexExpr struct {
	Left  Expr
	Index Expr
	P     Position
}

// SliceExpr is a slice: xs[1:3], xs[:2], xs[1:].
type SliceExpr struct {
	Left    Expr
	Low, Hi Expr
	HasLow  bool
	HasHigh bool
	P       Position
}

// PropExpr is property access: value.field.
type PropExpr struct {
	Left Expr
	Name string
	P    Position
}

// CallExpr is a call: f(a, b).
type CallExpr struct {
	Callee Expr
	Args   []Expr
	P      Position
}

// MethodExpr is a method call on a value or module: strings.upper(s).
type MethodExpr struct {
	Receiver Expr
	Name     string
	Args     []Expr
	P        Position
}

// PrefixExpr applies a unary operator: -x, not x.
type PrefixExpr struct {
	Op    TokenType
	Right Expr
	P     Position
}

// InfixExpr applies a binary operator: a + b.
type InfixExpr struct {
	Op    TokenType
	Left  Expr
	Right Expr
	P     Position
}

// RangeExpr is `from..to` (inclusive) or `from..to..step`.
type RangeExpr struct {
	Low, High, Step Expr
	P               Position
}

// TernaryExpr is cond ? a : b.
type TernaryExpr struct {
	Cond, Conseq, Alt Expr
	P                 Position
}

// FnExpr is a function literal or a named declaration.
type FnExpr struct {
	Name   string
	Params []Param
	// Ret is the optional return type annotation.
	Ret   *TypeExpr
	Body  *Block
	Arrow bool // written as fn(x) => expr
	P     Position
}

// GroupExpr is a parenthesised expression.
type GroupExpr struct {
	Inner Expr
	P     Position
}

// Param is one function parameter.
type Param struct {
	Name    string
	Type    *TypeExpr
	Default Expr
}

// ---- statements ----

// Stmt is a statement node.
type Stmt interface {
	Node
	stmt()
}

// VarDecl declares variables: int $n = 72, $a = 1.
type VarDecl struct {
	Name  string
	Type  *TypeExpr // nil when dynamically typed
	Value Expr      // nil for a bare declaration
	P     Position
}

// AssignStmt assigns to a variable or an index/property target.
type AssignStmt struct {
	Target Expr
	Op     TokenType
	Value  Expr
	P      Position
}

// ExprStmt evaluates an expression for its side effects.
type ExprStmt struct {
	X Expr
	P Position
}

// PrintStmt prints values: print "a", $b.
type PrintStmt struct {
	Values  []Expr
	Newline bool
	P       Position
}

// ReturnStmt returns from the current function.
type ReturnStmt struct {
	Value Expr
	P     Position
}

// IfStmt is if / else if / else.
type IfStmt struct {
	Cond Expr
	Then *Block
	Else Stmt
	P    Position
}

// WhileStmt is a while loop.
type WhileStmt struct {
	Cond Expr
	Body *Block
	P    Position
}

// ForSpec describes the source of a `for x in ...` loop.
type ForSpec struct {
	Low, High, Step Expr // range form, inclusive high
	IsRange         bool
	Src             Expr // list or object form
}

// ForInStmt is a for-in loop.
type ForInStmt struct {
	Var  string
	Var2 string // second loop variable for objects
	Spec ForSpec
	Body *Block
	P    Position
}

// FnDecl declares a named function.
type FnDecl struct {
	Fn *FnExpr
	P  Position
}

// BreakStmt breaks the innermost loop.
type BreakStmt struct{ P Position }

// ContinueStmt continues the innermost loop.
type ContinueStmt struct{ P Position }

// ThrowStmt raises an error: throw "boom".
type ThrowStmt struct {
	Value Expr
	P     Position
}

// TryStmt is try / catch e / finally.
type TryStmt struct {
	Body    *Block
	Var     string
	HasVar  bool
	Catch   *Block
	Finally *Block
	P       Position
}

// UseStmt imports a builtin module: use "strings".
type UseStmt struct {
	Path string
	P    Position
}

// Block is a brace-delimited list of statements.
type Block struct {
	Stmts []Stmt
	P     Position
}

func (n *Ident) Pos() Position       { return n.P }
func (n *IntLit) Pos() Position      { return n.P }
func (n *FloatLit) Pos() Position    { return n.P }
func (n *StrLit) Pos() Position      { return n.P }
func (n *BoolLit) Pos() Position     { return n.P }
func (n *NullLit) Pos() Position     { return n.P }
func (n *ListLit) Pos() Position     { return n.P }
func (n *ObjectLit) Pos() Position   { return n.P }
func (n *IndexExpr) Pos() Position   { return n.P }
func (n *SliceExpr) Pos() Position   { return n.P }
func (n *PropExpr) Pos() Position    { return n.P }
func (n *CallExpr) Pos() Position    { return n.P }
func (n *MethodExpr) Pos() Position  { return n.P }
func (n *PrefixExpr) Pos() Position  { return n.P }
func (n *InfixExpr) Pos() Position   { return n.P }
func (n *RangeExpr) Pos() Position   { return n.P }
func (n *TernaryExpr) Pos() Position { return n.P }
func (n *FnExpr) Pos() Position      { return n.P }
func (n *GroupExpr) Pos() Position   { return n.P }

func (n *VarDecl) Pos() Position      { return n.P }
func (n *AssignStmt) Pos() Position   { return n.P }
func (n *ExprStmt) Pos() Position     { return n.P }
func (n *PrintStmt) Pos() Position    { return n.P }
func (n *ReturnStmt) Pos() Position   { return n.P }
func (n *IfStmt) Pos() Position       { return n.P }
func (n *WhileStmt) Pos() Position    { return n.P }
func (n *ForInStmt) Pos() Position    { return n.P }
func (n *FnDecl) Pos() Position       { return n.P }
func (n *BreakStmt) Pos() Position    { return n.P }
func (n *ContinueStmt) Pos() Position { return n.P }
func (n *ThrowStmt) Pos() Position    { return n.P }
func (n *TryStmt) Pos() Position      { return n.P }
func (n *UseStmt) Pos() Position      { return n.P }
func (n *Block) Pos() Position        { return n.P }

func (*Ident) expr()       {}
func (*IntLit) expr()      {}
func (*FloatLit) expr()    {}
func (*StrLit) expr()      {}
func (*BoolLit) expr()     {}
func (*NullLit) expr()     {}
func (*ListLit) expr()     {}
func (*ObjectLit) expr()   {}
func (*IndexExpr) expr()   {}
func (*SliceExpr) expr()   {}
func (*PropExpr) expr()    {}
func (*CallExpr) expr()    {}
func (*MethodExpr) expr()  {}
func (*PrefixExpr) expr()  {}
func (*InfixExpr) expr()   {}
func (*RangeExpr) expr()   {}
func (*TernaryExpr) expr() {}
func (*FnExpr) expr()      {}
func (*GroupExpr) expr()   {}

func (*VarDecl) stmt()      {}
func (*AssignStmt) stmt()   {}
func (*ExprStmt) stmt()     {}
func (*PrintStmt) stmt()    {}
func (*ReturnStmt) stmt()   {}
func (*IfStmt) stmt()       {}
func (*WhileStmt) stmt()    {}
func (*ForInStmt) stmt()    {}
func (*FnDecl) stmt()       {}
func (*BreakStmt) stmt()    {}
func (*ContinueStmt) stmt() {}
func (*ThrowStmt) stmt()    {}
func (*TryStmt) stmt()      {}
func (*UseStmt) stmt()      {}
func (*Block) stmt()        {}

// node marks every AST node as part of the Node interface.
func (*TypeExpr) node()     {}
func (*Ident) node()        {}
func (*IntLit) node()       {}
func (*FloatLit) node()     {}
func (*StrLit) node()       {}
func (*BoolLit) node()      {}
func (*NullLit) node()      {}
func (*ListLit) node()      {}
func (*ObjectLit) node()    {}
func (*IndexExpr) node()    {}
func (*SliceExpr) node()    {}
func (*PropExpr) node()     {}
func (*CallExpr) node()     {}
func (*MethodExpr) node()   {}
func (*PrefixExpr) node()   {}
func (*InfixExpr) node()    {}
func (*RangeExpr) node()    {}
func (*TernaryExpr) node()  {}
func (*FnExpr) node()       {}
func (*GroupExpr) node()    {}
func (*Param) node()        {}
func (*VarDecl) node()      {}
func (*AssignStmt) node()   {}
func (*ExprStmt) node()     {}
func (*PrintStmt) node()    {}
func (*ReturnStmt) node()   {}
func (*IfStmt) node()       {}
func (*WhileStmt) node()    {}
func (*ForInStmt) node()    {}
func (*FnDecl) node()       {}
func (*BreakStmt) node()    {}
func (*ContinueStmt) node() {}
func (*ThrowStmt) node()    {}
func (*TryStmt) node()      {}
func (*UseStmt) node()      {}
func (*Block) node()        {}
