package biscuit

import (
	"fmt"
	"strconv"
)

type parser struct {
	source string
	toks   []token
	pos    int
}

func newParser(source string) (*parser, error) {
	l, err := lex(source)
	if err != nil {
		return nil, err
	}
	return &parser{source: source, toks: l.toks}, nil
}

func (p *parser) peek() token { return p.toks[p.pos] }

func (p *parser) next() token {
	tok := p.toks[p.pos]
	if tok.kind != tEOF {
		p.pos++
	}
	return tok
}

func (p *parser) errorf(tok token, format string, args ...any) error {
	return &ParseError{Line: tok.line, Col: tok.col, Msg: fmt.Sprintf(format, args...)}
}

// ParseBlock 解析一个块的文本。支持的语句：
//
//	pred(常量…)
//	head(…) :- body(…), body(…)
//	check if query(…) [|| query(…)]
//
// 块文本中不允许出现 allow / deny 语句，它们属于服务端策略，
// 请使用 ParsePolicies 解析。
func ParseBlock(source string) (*Block, error) {
	p, err := newParser(source)
	if err != nil {
		return nil, err
	}
	block := &Block{}
	for p.peek().kind != tEOF {
		tok := p.peek()
		switch {
		case tok.kind == tIdent && tok.text == keywordCheck:
			check, err := p.parseCheck()
			if err != nil {
				return nil, err
			}
			block.Checks = append(block.Checks, check)
		case tok.kind == tIdent && (tok.text == keywordAllow || tok.text == keywordDeny):
			return nil, p.errorf(tok, "%s statements belong to the verifier policies; use ParsePolicies", tok.text)
		default:
			fact, rule, err := p.parseFactOrRule()
			if err != nil {
				return nil, err
			}
			if rule != nil {
				if err := validateRule(*rule); err != nil {
					return nil, err
				}
				block.Rules = append(block.Rules, *rule)
			} else {
				if err := validateGroundFact(fact); err != nil {
					return nil, err
				}
				block.Facts = append(block.Facts, fact)
			}
		}
		p.consumeTerminator()
	}
	return block, nil
}

// ParsePolicies 解析服务端 allow / deny 策略文本，例如：
//
//	allow if right($user, "read")
//	deny if revoked($user)
//	allow if true
//
// 策略查询只能使用 authority 作用域内的事实（块 0 的事实与规则推出的事实），
// 衰减块推出的事实永远不会进入该作用域。
func ParsePolicies(source string) ([]Policy, error) {
	p, err := newParser(source)
	if err != nil {
		return nil, err
	}
	var policies []Policy
	for p.peek().kind != tEOF {
		tok := p.peek()
		if tok.kind != tIdent || (tok.text != keywordAllow && tok.text != keywordDeny) {
			return nil, p.errorf(tok, "expected allow or deny policy, got %s", tok.String())
		}
		policy, err := p.parsePolicy()
		if err != nil {
			return nil, err
		}
		policies = append(policies, policy)
		p.consumeTerminator()
	}
	return policies, nil
}

const (
	keywordCheck = "check"
	keywordIf    = "if"
	keywordAllow = "allow"
	keywordDeny  = "deny"
	keywordOr    = "or"
)

func (p *parser) consumeTerminator() {
	for p.peek().kind == tSemicolon {
		p.next()
	}
}

func (p *parser) expect(kind tokenKind, what string) (token, error) {
	tok := p.next()
	if tok.kind != kind {
		return tok, p.errorf(tok, "expected %s, got %s", what, tok.String())
	}
	return tok, nil
}

func (p *parser) expectIdent(word string) error {
	tok := p.next()
	if tok.kind != tIdent || tok.text != word {
		return p.errorf(tok, "expected %q, got %s", word, tok.String())
	}
	return nil
}

func (p *parser) parseCheck() (Check, error) {
	checkTok := p.next() // check
	if err := p.expectIdent(keywordIf); err != nil {
		return Check{}, err
	}
	queries, err := p.parseQueryList()
	if err != nil {
		return Check{}, err
	}
	for _, query := range queries {
		if err := validateRule(query); err != nil {
			return Check{}, err
		}
	}
	_ = checkTok
	return Check{Queries: queries}, nil
}

func (p *parser) parsePolicy() (Policy, error) {
	headTok := p.next() // allow / deny
	kind := PolicyAllow
	if headTok.text == keywordDeny {
		kind = PolicyDeny
	}
	if err := p.expectIdent(keywordIf); err != nil {
		return Policy{}, err
	}
	queries, err := p.parseQueryList()
	if err != nil {
		return Policy{}, err
	}
	for _, query := range queries {
		if err := validateRule(query); err != nil {
			return Policy{}, err
		}
	}
	return Policy{Kind: kind, Queries: queries}, nil
}

// parseQueryList 解析以 || 或 or 分隔的查询列表，每个查询是一条无 head 的规则体。
func (p *parser) parseQueryList() ([]Rule, error) {
	var queries []Rule
	for {
		if tok := p.peek(); tok.kind == tIdent && tok.text == "true" {
			p.next()
			// 空规则体恒真，不依赖任何事实。
			queries = append(queries, Rule{})
		} else {
			body, err := p.parseRuleBody()
			if err != nil {
				return nil, err
			}
			if len(body) == 0 {
				return nil, p.errorf(p.peek(), "expected at least one predicate after if")
			}
			queries = append(queries, Rule{Body: body})
		}
		sep := p.peek()
		if sep.kind == tOr {
			p.next()
			continue
		}
		if sep.kind == tIdent && sep.text == keywordOr {
			p.next()
			continue
		}
		return queries, nil
	}
}

// parseFactOrRule 解析事实或规则，返回值 rule 非 nil 表示是规则。
func (p *parser) parseFactOrRule() (Fact, *Rule, error) {
	head, err := p.parsePredicate()
	if err != nil {
		return Fact{}, nil, err
	}
	if p.peek().kind != tColon {
		return head, nil, nil
	}
	p.next() // 消费 :-
	body, err := p.parseRuleBody()
	if err != nil {
		return Fact{}, nil, err
	}
	if len(body) == 0 {
		return Fact{}, nil, p.errorf(p.peek(), "rule must have at least one body predicate")
	}
	return Fact{}, &Rule{Head: head, Body: body}, nil
}

func (p *parser) parseRuleBody() ([]Fact, error) {
	var atoms []Fact
	for {
		predicate, err := p.parsePredicate()
		if err != nil {
			return nil, err
		}
		atoms = append(atoms, predicate)
		if p.peek().kind != tComma {
			return atoms, nil
		}
		p.next() // 消费逗号
	}
}

func (p *parser) parsePredicate() (Fact, error) {
	nameTok := p.next()
	if nameTok.kind != tIdent {
		return Fact{}, p.errorf(nameTok, "expected predicate name, got %s", nameTok.String())
	}
	if _, err := p.expect(tLParen, "("); err != nil {
		return Fact{}, err
	}
	var args []Term
	if p.peek().kind == tRParen {
		p.next()
		return Fact{Predicate: nameTok.text, Args: args}, nil
	}
	for {
		term, err := p.parseTerm()
		if err != nil {
			return Fact{}, err
		}
		args = append(args, term)
		sep := p.next()
		switch sep.kind {
		case tComma:
			continue
		case tRParen:
			return Fact{Predicate: nameTok.text, Args: args}, nil
		default:
			return Fact{}, p.errorf(sep, "expected , or ), got %s", sep.String())
		}
	}
}

func (p *parser) parseTerm() (Term, error) {
	tok := p.next()
	switch tok.kind {
	case tVariable:
		return Variable(tok.text), nil
	case tString:
		return String(tok.text), nil
	case tInteger:
		value, err := strconv.ParseInt(tok.text, 10, 64)
		if err != nil {
			return Term{}, p.errorf(tok, "invalid integer %q: %v", tok.text, err)
		}
		return Integer(value), nil
	case tIdent:
		switch tok.text {
		case "true":
			return Bool(true), nil
		case "false":
			return Bool(false), nil
		default:
			return Symbol(tok.text), nil
		}
	default:
		return Term{}, p.errorf(tok, "expected a term, got %s", tok.String())
	}
}

func validateGroundFact(fact Fact) error {
	for _, arg := range fact.Args {
		if arg.IsVariable() {
			return fmt.Errorf("fact %s must not contain variables", fact.String())
		}
	}
	return nil
}

// validateRule 执行 Datalog 安全性检查：head 中出现的每个变量都必须在 body 中出现。
func validateRule(rule Rule) error {
	bound := map[string]bool{}
	for _, atom := range rule.Body {
		for _, arg := range atom.Args {
			if arg.IsVariable() {
				bound[arg.Name] = true
			}
		}
	}
	for _, arg := range rule.Head.Args {
		if arg.IsVariable() && !bound[arg.Name] {
			return fmt.Errorf("rule %s is unsafe: head variable $%s does not appear in the body", rule.String(), arg.Name)
		}
	}
	return nil
}
