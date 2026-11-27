package scim

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// 解析器安全上限的默认值。
const (
	// DefaultMaxASTDepth 限制嵌套括号 / not / 值路径以及二叉 AST 的最大深度。
	DefaultMaxASTDepth = 32
	// DefaultMaxNodes 限制单条表达式的 AST 节点总数。
	DefaultMaxNodes = 1024
	// DefaultMaxFilterLength 限制 filter 原始输入长度（字节）。
	DefaultMaxFilterLength = 64 * 1024
)

type parseConfig struct {
	maxDepth       int
	maxNodes       int
	maxInputLength int
}

// Option 配置解析器安全参数。
type Option func(*parseConfig)

// WithMaxASTDepth 设置最大 AST / 嵌套深度。
func WithMaxASTDepth(n int) Option {
	return func(c *parseConfig) {
		if n > 0 {
			c.maxDepth = n
		}
	}
}

// WithMaxNodes 设置最大 AST 节点数。
func WithMaxNodes(n int) Option {
	return func(c *parseConfig) {
		if n > 0 {
			c.maxNodes = n
		}
	}
}

// WithMaxInputLength 设置允许的最大输入字节数。
func WithMaxInputLength(n int) Option {
	return func(c *parseConfig) {
		if n > 0 {
			c.maxInputLength = n
		}
	}
}

func defaultParseConfig() parseConfig {
	return parseConfig{
		maxDepth:       DefaultMaxASTDepth,
		maxNodes:       DefaultMaxNodes,
		maxInputLength: DefaultMaxFilterLength,
	}
}

// ParseFilter 以递归下降方式解析 RFC 7644 §3.4.2.2 filter 表达式。
//
// 支持全部十个属性操作符（eq/ne/co/sw/ew/pr/gt/ge/lt/le）、
// and/or/not 逻辑、括号分组以及 attr[subFilter] 值路径。
// 词法层面即严格区分字符串字面量，任何引号内的 OR/括号都只是数据，
// 从根本上避免条件拼接注入。
func ParseFilter(input string, opts ...Option) (Expr, error) {
	cfg := defaultParseConfig()
	for _, opt := range opts {
		opt(&cfg)
	}
	return parseFilter(input, cfg)
}

// Parse 是 ParseFilter 的简短别名。
func Parse(input string, opts ...Option) (Expr, error) {
	return ParseFilter(input, opts...)
}

// Parser 是携带固定安全配置的可复用 SCIM filter 解析器（并发安全：
// 其配置在构造后不再变化，每次 Parse 使用独立状态）。
type Parser struct {
	cfg parseConfig
}

// NewParser 创建解析器。
func NewParser(opts ...Option) *Parser {
	cfg := defaultParseConfig()
	for _, opt := range opts {
		opt(&cfg)
	}
	return &Parser{cfg: cfg}
}

// Parse 解析一条 SCIM filter。
func (p *Parser) Parse(input string) (Expr, error) {
	return parseFilter(input, p.cfg)
}

func parseFilter(input string, cfg parseConfig) (Expr, error) {
	if len(input) > cfg.maxInputLength {
		return nil, fmt.Errorf("%w: input length %d exceeds limit %d",
			ErrFilterComplexity, len(input), cfg.maxInputLength)
	}

	toks, err := lexSCIM(input)
	if err != nil {
		var le *LexError
		if errors.As(err, &le) {
			return nil, fmt.Errorf("%w: %w", ErrInvalidFilter, le)
		}
		return nil, fmt.Errorf("%w: %v", ErrInvalidFilter, err)
	}

	p := &parser{toks: toks, maxDepth: cfg.maxDepth}
	expr, err := p.parseOr(1)
	if err != nil {
		return nil, err
	}
	if t := p.cur(); t.kind != tokEOF {
		return nil, p.errorf(t.pos, "unexpected %s %q", t.kind, t.lit)
	}
	if d := expr.Depth(); d > cfg.maxDepth {
		return nil, fmt.Errorf("%w: AST depth %d exceeds limit %d",
			ErrFilterDepth, d, cfg.maxDepth)
	}
	if n := CountNodes(expr); n > cfg.maxNodes {
		return nil, fmt.Errorf("%w: AST node count %d exceeds limit %d",
			ErrFilterComplexity, n, cfg.maxNodes)
	}
	return expr, nil
}

type parser struct {
	toks     []token
	idx      int
	maxDepth int
	sentinel error
}

func (p *parser) errorSentinel() error {
	if p.sentinel != nil {
		return p.sentinel
	}
	return ErrInvalidFilter
}

func (p *parser) cur() token {
	if p.idx >= len(p.toks) {
		return token{kind: tokEOF}
	}
	return p.toks[p.idx]
}

func (p *parser) advance() token {
	t := p.cur()
	if t.kind != tokEOF {
		p.idx++
	}
	return t
}

func (p *parser) errorf(pos int, format string, args ...any) error {
	return fmt.Errorf("%w: byte %d: %s",
		p.errorSentinel(), pos, fmt.Sprintf(format, args...))
}

func isKeyword(t token, kw string) bool {
	return t.kind == tokIdent && strings.EqualFold(t.lit, kw)
}

func (p *parser) consumeKeyword(kw string) bool {
	if isKeyword(p.cur(), kw) {
		p.advance()
		return true
	}
	return false
}

// parseOr 实现 "or" 优先级（最低）。
func (p *parser) parseOr(depth int) (Expr, error) {
	if depth > p.maxDepth {
		return nil, fmt.Errorf("%w: nesting exceeds limit %d", ErrFilterDepth, p.maxDepth)
	}
	left, err := p.parseAnd(depth)
	if err != nil {
		return nil, err
	}
	for isKeyword(p.cur(), "or") {
		opTok := p.advance()
		right, err := p.parseAnd(depth)
		if err != nil {
			return nil, err
		}
		left = &LogicalExpr{Op: LogicalOr, Left: left, Right: right}
		_ = opTok
	}
	return left, nil
}

// parseAnd 实现 "and" 优先级（高于 or）。
func (p *parser) parseAnd(depth int) (Expr, error) {
	left, err := p.parseNot(depth)
	if err != nil {
		return nil, err
	}
	for isKeyword(p.cur(), "and") {
		p.advance()
		right, err := p.parseNot(depth)
		if err != nil {
			return nil, err
		}
		left = &LogicalExpr{Op: LogicalAnd, Left: left, Right: right}
	}
	return left, nil
}

// parseNot 实现前缀 not，兼容 RFC 的 not(...) 与宽松的 not expr。
func (p *parser) parseNot(depth int) (Expr, error) {
	if depth > p.maxDepth {
		return nil, fmt.Errorf("%w: nesting exceeds limit %d", ErrFilterDepth, p.maxDepth)
	}
	if isKeyword(p.cur(), "not") {
		p.advance()
		inner, err := p.parseNot(depth + 1)
		if err != nil {
			return nil, err
		}
		return &NotExpr{Inner: inner}, nil
	}
	return p.parsePrimary(depth)
}

// parsePrimary 处理括号分组、值路径与属性表达式。
func (p *parser) parsePrimary(depth int) (Expr, error) {
	t := p.cur()

	if t.kind == tokLParen {
		p.advance()
		inner, err := p.parseOr(depth + 1)
		if err != nil {
			return nil, err
		}
		if ct := p.cur(); ct.kind != tokRParen {
			return nil, p.errorf(ct.pos, "expected ')' but found %s %q", ct.kind, ct.lit)
		}
		p.advance()
		return inner, nil
	}

	if t.kind != tokIdent {
		return nil, p.errorf(t.pos, "expected attribute path or '(' but found %s %q", t.kind, t.lit)
	}

	path, err := p.parseAttrPath()
	if err != nil {
		return nil, err
	}

	// attr[subFilter] 值路径。
	if p.cur().kind == tokLBracket {
		p.advance()
		inner, err := p.parseOr(depth + 1)
		if err != nil {
			return nil, err
		}
		if ct := p.cur(); ct.kind != tokRBracket {
			return nil, p.errorf(ct.pos, "expected ']' to close value path but found %s %q",
				ct.kind, ct.lit)
		}
		p.advance()
		return &ValuePathExpr{Path: path, Filter: inner}, nil
	}

	// 属性表达式：path pr 或 path op value。
	opTok := p.cur()
	if opTok.kind != tokIdent {
		return nil, p.errorf(opTok.pos,
			"expected SCIM operator (eq/ne/co/sw/ew/pr/gt/ge/lt/le) after %q", path.String())
	}
	op := Operator(strings.ToLower(opTok.lit))
	if op == OpPr {
		p.advance()
		return &AttrExpr{Path: path, Op: OpPr}, nil
	}
	if !op.IsBinary() {
		return nil, p.errorf(opTok.pos, "unknown SCIM operator %q", opTok.lit)
	}
	p.advance()

	valTok := p.cur()
	lit, err := literalFromToken(valTok)
	if err != nil {
		return nil, p.errorf(valTok.pos, "expected comparison value after %q: %v", opTok.lit, err)
	}
	p.advance()

	return &AttrExpr{Path: path, Op: op, Value: lit}, nil
}

// parseAttrPath 消费一个属性路径。常规点分/URN 路径在词法层已是单个
// identifier token；tokDot 分支用于括号路径之后的 .subAttr 等场景。
func (p *parser) parseAttrPath() (AttrPath, error) {
	t := p.advance()
	if t.kind != tokIdent {
		return AttrPath{}, p.errorf(t.pos, "expected attribute path but found %s %q", t.kind, t.lit)
	}
	raw := t.lit
	for p.cur().kind == tokDot {
		dot := p.advance()
		nameTok := p.cur()
		if nameTok.kind != tokIdent {
			return AttrPath{}, p.errorf(dot.pos, "expected attribute name after '.'")
		}
		raw += "." + nameTok.lit
		p.advance()
	}
	ap, err := ParseAttrPath(raw)
	if err != nil {
		return AttrPath{}, p.errorf(t.pos, "%v", err)
	}
	return ap, nil
}

func literalFromToken(t token) (*Literal, error) {
	switch t.kind {
	case tokString:
		var s string
		if err := json.Unmarshal([]byte(t.lit), &s); err != nil {
			return nil, err
		}
		return &Literal{Kind: LitString, Value: s, Raw: t.lit}, nil
	case tokNumber:
		return numberLiteral(t.lit)
	case tokIdent:
		switch strings.ToLower(t.lit) {
		case "true":
			return boolLiteral(true), nil
		case "false":
			return boolLiteral(false), nil
		case "null":
			return nullLiteral(), nil
		}
	}
	return nil, fmt.Errorf("expected string, number, true, false or null but found %s %q", t.kind, t.lit)
}
