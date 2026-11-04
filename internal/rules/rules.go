// Package rules parses PocketBase-inspired filter expressions and compiles
// them to SQL WHERE fragments (for list/view) or evaluates them in Go (for
// create/update/delete record checks).
package rules

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Auth is the caller context exposed to rules as @request.auth.*.
type Auth struct {
	LoggedIn   bool
	UserID     string
	DB         string
	Collection string
	Email      string
}

// Schema maps record field names to their logical types (text, number, ...).
// "id", "created" and "updated" are always addressable.
type Schema map[string]string

var identRe = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]{0,63}$`)

// reserved holds the always-available record columns.
var reserved = map[string]string{"id": "text", "created": "text", "updated": "text"}

func fieldType(schema Schema, name string) (string, bool) {
	if t, ok := reserved[name]; ok {
		return t, true
	}
	t, ok := schema[name]
	return t, ok
}

// --- tokenizer ---

type tokKind int

const (
	tEOF tokKind = iota
	tIdent
	tString
	tNumber
	tParam
	tOp
	tLParen
	tRParen
)

type token struct {
	kind tokKind
	val  string
}

func lex(s string) ([]token, error) {
	var out []token
	i := 0
	for i < len(s) {
		c := s[i]
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			i++
		case c == '(':
			out = append(out, token{tLParen, "("})
			i++
		case c == ')':
			out = append(out, token{tRParen, ")"})
			i++
		case c == '\'' || c == '"':
			quote := c
			i++
			var b strings.Builder
			for i < len(s) && s[i] != quote {
				if s[i] == '\\' && i+1 < len(s) {
					i++
				}
				b.WriteByte(s[i])
				i++
			}
			if i >= len(s) {
				return nil, fmt.Errorf("unterminated string")
			}
			i++
			out = append(out, token{tString, b.String()})
		case c == '{':
			if i+1 < len(s) && s[i+1] == ':' {
				j := strings.IndexByte(s[i+2:], '}')
				if j < 0 {
					return nil, fmt.Errorf("unterminated parameter")
				}
				name := s[i+2 : i+2+j]
				if !identRe.MatchString(name) {
					return nil, fmt.Errorf("invalid parameter name %q", name)
				}
				out = append(out, token{tParam, name})
				i = i + 2 + j + 1
			} else {
				return nil, fmt.Errorf("unexpected '{'")
			}
		case c >= '0' && c <= '9' || (c == '-' && i+1 < len(s) && s[i+1] >= '0' && s[i+1] <= '9'):
			j := i + 1
			for j < len(s) && (s[j] >= '0' && s[j] <= '9' || s[j] == '.') {
				j++
			}
			out = append(out, token{tNumber, s[i:j]})
			i = j
		case isIdentStart(c):
			j := i + 1
			for j < len(s) && isIdentPart(s[j]) {
				j++
			}
			out = append(out, token{tIdent, s[i:j]})
			i = j
		default:
			// operators, longest match first
			two := ""
			if i+1 < len(s) {
				two = s[i : i+2]
			}
			switch two {
			case "&&", "||", "==", "!=", ">=", "<=", "!~":
				out = append(out, token{tOp, two})
				i += 2
				continue
			}
			if strings.ContainsRune("=<>~!", rune(c)) {
				out = append(out, token{tOp, string(c)})
				i++
				continue
			}
			return nil, fmt.Errorf("unexpected character %q", string(c))
		}
	}
	out = append(out, token{tEOF, ""})
	return out, nil
}

func isIdentStart(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c == '_' || c == '@'
}

func isIdentPart(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '.' || c == '@'
}

// --- AST ---

type node interface{}

type litNode struct{ v any }
type identNode struct{ name string }
type reqNode struct{ path []string }
type unaryNode struct {
	op string
	x  node
}
type binNode struct {
	op   string
	l, r node
}

// --- parser ---

type parser struct {
	toks []token
	pos  int
}

func (p *parser) peek() token { return p.toks[p.pos] }
func (p *parser) next() token { t := p.toks[p.pos]; p.pos++; return t }

func parse(s string) (node, error) {
	toks, err := lex(s)
	if err != nil {
		return nil, err
	}
	p := &parser{toks: toks}
	n, err := p.parseOr()
	if err != nil {
		return nil, err
	}
	if p.peek().kind != tEOF {
		return nil, fmt.Errorf("unexpected token %q", p.peek().val)
	}
	return n, nil
}

func (p *parser) parseOr() (node, error) {
	left, err := p.parseAnd()
	if err != nil {
		return nil, err
	}
	for p.peek().kind == tOp && p.peek().val == "||" {
		p.next()
		right, err := p.parseAnd()
		if err != nil {
			return nil, err
		}
		left = binNode{"||", left, right}
	}
	return left, nil
}

func (p *parser) parseAnd() (node, error) {
	left, err := p.parseUnary()
	if err != nil {
		return nil, err
	}
	for p.peek().kind == tOp && p.peek().val == "&&" {
		p.next()
		right, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		left = binNode{"&&", left, right}
	}
	return left, nil
}

func (p *parser) parseUnary() (node, error) {
	if p.peek().kind == tOp && p.peek().val == "!" {
		p.next()
		x, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		return unaryNode{"!", x}, nil
	}
	return p.parseComparison()
}

func (p *parser) parseComparison() (node, error) {
	left, err := p.parsePrimary()
	if err != nil {
		return nil, err
	}
	if p.peek().kind == tOp && isCompare(p.peek().val) {
		op := p.next().val
		right, err := p.parsePrimary()
		if err != nil {
			return nil, err
		}
		return binNode{op, left, right}, nil
	}
	return left, nil
}

func isCompare(op string) bool {
	switch op {
	case "=", "==", "!=", "<", "<=", ">", ">=", "~", "!~":
		return true
	}
	return false
}

func (p *parser) parsePrimary() (node, error) {
	t := p.next()
	switch t.kind {
	case tLParen:
		n, err := p.parseOr()
		if err != nil {
			return nil, err
		}
		if p.peek().kind != tRParen {
			return nil, fmt.Errorf("missing closing parenthesis")
		}
		p.next()
		return n, nil
	case tString:
		return litNode{t.val}, nil
	case tNumber:
		f, err := strconv.ParseFloat(t.val, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid number %q", t.val)
		}
		return litNode{f}, nil
	case tParam:
		return paramNode{t.val}, nil
	case tIdent:
		switch t.val {
		case "true":
			return litNode{true}, nil
		case "false":
			return litNode{false}, nil
		case "null":
			return litNode{nil}, nil
		}
		if strings.HasPrefix(t.val, "@request") {
			parts := strings.Split(t.val, ".")
			if parts[0] != "@request" {
				return nil, fmt.Errorf("invalid request reference %q", t.val)
			}
			if len(parts) < 2 || parts[1] != "auth" {
				return nil, fmt.Errorf("unsupported request reference %q", t.val)
			}
			return reqNode{parts[2:]}, nil
		}
		if !identRe.MatchString(t.val) {
			return nil, fmt.Errorf("invalid identifier %q", t.val)
		}
		return identNode{t.val}, nil
	}
	return nil, fmt.Errorf("unexpected token %q", t.val)
}

type paramNode struct{ name string }

// --- SQL compiler ---

type compiler struct {
	schema Schema
	auth   *Auth
	params map[string]string
	args   []any
}

// Compile turns a rule/filter expression into a SQL boolean fragment.
// Special values: "*" (public), "user" (any authenticated user), "" (nobody).
// Placeholders like {:name} are substituted from params.
func Compile(expr string, schema Schema, auth *Auth, params map[string]string) (string, []any, error) {
	expr = strings.TrimSpace(expr)
	switch expr {
	case "*":
		return "1=1", nil, nil
	case "user":
		if auth != nil && auth.LoggedIn {
			return "1=1", nil, nil
		}
		return "1=0", nil, nil
	case "":
		return "1=0", nil, nil
	}
	n, err := parse(expr)
	if err != nil {
		return "", nil, err
	}
	c := &compiler{schema: schema, auth: auth, params: params}
	cond, err := c.value(n, true)
	if err != nil {
		return "", nil, err
	}
	return cond, c.args, nil
}

func (c *compiler) addArg(v any) string {
	c.args = append(c.args, v)
	return "?"
}

func (c *compiler) value(n node, sql bool) (string, error) {
	switch t := n.(type) {
	case litNode:
		return c.addArg(t.v), nil
	case paramNode:
		v := ""
		if c.params != nil {
			v = c.params[t.name]
		}
		return c.addArg(v), nil
	case identNode:
		if _, ok := fieldType(c.schema, t.name); !ok {
			// Bareword on the right-hand side: treat as a string literal so
			// legacy filters such as "judul=Buku" keep working.
			return c.addArg(t.name), nil
		}
		return `"` + t.name + `"`, nil
	case reqNode:
		return c.addArg(c.reqValue(t.path)), nil
	case unaryNode:
		inner, err := c.value(t.x, true)
		if err != nil {
			return "", err
		}
		return "(NOT " + inner + ")", nil
	case binNode:
		return c.binary(t)
	}
	return "", fmt.Errorf("unsupported expression")
}

func (c *compiler) reqValue(path []string) any {
	if c.auth == nil || !c.auth.LoggedIn {
		return ""
	}
	if len(path) == 0 {
		return c.auth.UserID
	}
	switch path[0] {
	case "id":
		return c.auth.UserID
	case "email":
		return c.auth.Email
	case "collectionName":
		return c.auth.Collection
	default:
		return ""
	}
}

func (c *compiler) binary(b binNode) (string, error) {
	switch b.op {
	case "&&", "||":
		l, err := c.value(b.l, true)
		if err != nil {
			return "", err
		}
		r, err := c.value(b.r, true)
		if err != nil {
			return "", err
		}
		joiner := " AND "
		if b.op == "||" {
			joiner = " OR "
		}
		return "(" + l + joiner + r + ")", nil
	case "~", "!~":
		l, err := c.value(b.l, true)
		if err != nil {
			return "", err
		}
		r, err := c.literalString(b.r)
		if err != nil {
			return "", err
		}
		op := " LIKE "
		if b.op == "!~" {
			op = " NOT LIKE "
		}
		return "(" + l + op + c.addArg("%"+r+"%") + ")", nil
	}

	// Comparison: handle NULL on either side and field/field comparisons.
	if isLitNil(b.l) || isLitNil(b.r) {
		other := b.l
		if isLitNil(b.l) {
			other = b.r
		}
		left, err := c.value(other, true)
		if err != nil {
			return "", err
		}
		if b.op == "!=" || b.op == "!~" {
			return "(" + left + " IS NOT NULL)", nil
		}
		return "(" + left + " IS NULL)", nil
	}
	l, err := c.value(b.l, true)
	if err != nil {
		return "", err
	}
	r, err := c.value(b.r, true)
	if err != nil {
		return "", err
	}
	op := b.op
	if op == "==" {
		op = "="
	}
	if op == "!~" {
		op = "!="
	}
	return "(" + l + " " + op + " " + r + ")", nil
}

func (c *compiler) literalString(n node) (string, error) {
	switch t := n.(type) {
	case litNode:
		return fmt.Sprint(t.v), nil
	case paramNode:
		if c.params == nil {
			return "", nil
		}
		return c.params[t.name], nil
	case identNode:
		return t.name, nil
	}
	return "", fmt.Errorf("expected a string operand")
}

func isLitNil(n node) bool {
	l, ok := n.(litNode)
	return ok && l.v == nil
}

// Eval evaluates an expression against a record (used for create/update/delete).
func Eval(expr string, rec map[string]any, schema Schema, auth *Auth) (bool, error) {
	expr = strings.TrimSpace(expr)
	switch expr {
	case "*":
		return true, nil
	case "user":
		return auth != nil && auth.LoggedIn, nil
	case "":
		return false, nil
	}
	n, err := parse(expr)
	if err != nil {
		return false, err
	}
	v, err := evalNode(n, rec, auth)
	if err != nil {
		return false, err
	}
	return truthy(v), nil
}

func evalNode(n node, rec map[string]any, auth *Auth) (any, error) {
	switch t := n.(type) {
	case litNode:
		return t.v, nil
	case paramNode:
		return nil, fmt.Errorf("parameters are only supported in list filters")
	case identNode:
		if v, ok := rec[t.name]; ok {
			return v, nil
		}
		return nil, nil
	case reqNode:
		if auth == nil || !auth.LoggedIn {
			return "", nil
		}
		if len(t.path) == 0 {
			return auth.UserID, nil
		}
		switch t.path[0] {
		case "id":
			return auth.UserID, nil
		case "email":
			return auth.Email, nil
		case "collectionName":
			return auth.Collection, nil
		}
		return "", nil
	case unaryNode:
		v, err := evalNode(t.x, rec, auth)
		if err != nil {
			return nil, err
		}
		return !truthy(v), nil
	case binNode:
		return evalBinary(t, rec, auth)
	}
	return nil, fmt.Errorf("unsupported expression")
}

func evalBinary(b binNode, rec map[string]any, auth *Auth) (any, error) {
	if b.op == "&&" || b.op == "||" {
		l, err := evalNode(b.l, rec, auth)
		if err != nil {
			return nil, err
		}
		if b.op == "&&" && !truthy(l) {
			return false, nil
		}
		if b.op == "||" && truthy(l) {
			return true, nil
		}
		r, err := evalNode(b.r, rec, auth)
		if err != nil {
			return nil, err
		}
		if b.op == "&&" {
			return truthy(l) && truthy(r), nil
		}
		return truthy(l) || truthy(r), nil
	}
	l, err := evalNode(b.l, rec, auth)
	if err != nil {
		return nil, err
	}
	r, err := evalNode(b.r, rec, auth)
	if err != nil {
		return nil, err
	}
	return compare(l, r, b.op), nil
}

func truthy(v any) bool {
	switch t := v.(type) {
	case nil:
		return false
	case bool:
		return t
	case string:
		return t != ""
	case float64:
		return t != 0
	case int:
		return t != 0
	case int64:
		return t != 0
	}
	return true
}

func num(v any) (float64, bool) {
	switch t := v.(type) {
	case float64:
		return t, true
	case float32:
		return float64(t), true
	case int:
		return float64(t), true
	case int64:
		return float64(t), true
	case interface{ Float64() (float64, error) }:
		f, err := t.Float64()
		return f, err == nil
	case string:
		f, err := strconv.ParseFloat(t, 64)
		return f, err == nil
	}
	return 0, false
}

func compare(l, r any, op string) bool {
	if op == "~" || op == "!~" {
		contains := strings.Contains(fmt.Sprint(l), fmt.Sprint(r))
		if op == "!~" {
			return !contains
		}
		return contains
	}
	if l == nil || r == nil {
		eq := l == nil && r == nil
		switch op {
		case "=", "==":
			return eq
		case "!=":
			return !eq
		}
		return false
	}
	if lb, ok := l.(bool); ok {
		rb, _ := r.(bool)
		switch op {
		case "=", "==":
			return lb == rb
		case "!=":
			return lb != rb
		}
	}
	if ln, ok := num(l); ok {
		if rn, ok := num(r); ok {
			switch op {
			case "=", "==":
				return ln == rn
			case "!=":
				return ln != rn
			case "<":
				return ln < rn
			case "<=":
				return ln <= rn
			case ">":
				return ln > rn
			case ">=":
				return ln >= rn
			}
		}
	}
	ls, rs := fmt.Sprint(l), fmt.Sprint(r)
	switch op {
	case "=", "==":
		return ls == rs
	case "!=":
		return ls != rs
	case "<":
		return ls < rs
	case "<=":
		return ls <= rs
	case ">":
		return ls > rs
	case ">=":
		return ls >= rs
	}
	return false
}
