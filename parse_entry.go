package netsec

import "fmt"

// ParseBlock 解析单个 Biscuit 块。index 为块在令牌中的位置（0 为 authority 块）。
// 块内只允许事实、规则与 check if；allow/deny 属于服务端策略，出现在块中会报错。
func ParseBlock(index int, src string) (Block, error) {
	tokens, err := lex(src)
	if err != nil {
		return Block{}, err
	}
	p := &parser{tokens: tokens}
	block := Block{Index: index}
	p.skipTerminators()
	for p.peek().kind != tkEOF {
		t := p.peek()
		if t.kind != tkIdent {
			return Block{}, fmt.Errorf("%w: line %d: unexpected token %q at start of statement", ErrParse, t.line, t.text)
		}
		switch t.text {
		case "check":
			body, err := p.parseCheck()
			if err != nil {
				return Block{}, err
			}
			if err := validateBodySafety(body); err != nil {
				return Block{}, err
			}
			block.Checks = append(block.Checks, Check{Body: body})
		case "allow", "deny":
			return Block{}, fmt.Errorf("%w: line %d: %s policies belong to the authorizer, not to token blocks", ErrParse, t.line, t.text)
		default:
			rule, err := p.parseFactOrRule(t.line)
			if err != nil {
				return Block{}, err
			}
			if rule.Body == nil {
				if !rule.Head.IsGround() {
					return Block{}, fmt.Errorf("%w: line %d: fact %s must not contain variables", ErrSafety, t.line, rule.Head.Name)
				}
				block.Facts = append(block.Facts, rule.Head)
			} else {
				if err := validateRuleSafety(rule); err != nil {
					return Block{}, err
				}
				block.Rules = append(block.Rules, rule)
			}
		}
		if err := p.expectTerminator(); err != nil {
			return Block{}, err
		}
	}
	return block, nil
}

// ParseToken 按顺序解析多个块源，第一个块是 authority 块，其余是衰减块。
func ParseToken(blockSources ...string) (Token, error) {
	if len(blockSources) == 0 {
		return Token{}, ErrEmptyToken
	}
	token := Token{Blocks: make([]Block, 0, len(blockSources))}
	for i, src := range blockSources {
		block, err := ParseBlock(i, src)
		if err != nil {
			return Token{}, fmt.Errorf("block %d: %w", i, err)
		}
		token.Blocks = append(token.Blocks, block)
	}
	return token, nil
}

// ParseAuthorizer 解析服务端授权器源：允许事实与 allow if / deny if 策略，
// 不允许规则与 check。
func ParseAuthorizer(src string) (Authorizer, error) {
	tokens, err := lex(src)
	if err != nil {
		return Authorizer{}, err
	}
	p := &parser{tokens: tokens}
	auth := Authorizer{}
	p.skipTerminators()
	for p.peek().kind != tkEOF {
		t := p.peek()
		if t.kind != tkIdent {
			return Authorizer{}, fmt.Errorf("%w: line %d: unexpected token %q", ErrParse, t.line, t.text)
		}
		switch t.text {
		case "check":
			return Authorizer{}, fmt.Errorf("%w: line %d: check is not allowed in the authorizer", ErrParse, t.line)
		case "allow", "deny":
			policy, err := p.parsePolicy()
			if err != nil {
				return Authorizer{}, err
			}
			if err := validateBodySafety(policy.Body); err != nil {
				return Authorizer{}, err
			}
			auth.Policies = append(auth.Policies, policy)
		default:
			rule, err := p.parseFactOrRule(t.line)
			if err != nil {
				return Authorizer{}, err
			}
			if rule.Body != nil {
				return Authorizer{}, fmt.Errorf("%w: line %d: rules are not allowed in the authorizer", ErrParse, t.line)
			}
			if !rule.Head.IsGround() {
				return Authorizer{}, fmt.Errorf("%w: line %d: fact %s must not contain variables", ErrSafety, t.line, rule.Head.Name)
			}
			auth.Facts = append(auth.Facts, rule.Head)
		}
		if err := p.expectTerminator(); err != nil {
			return Authorizer{}, err
		}
	}
	return auth, nil
}

// validateRuleSafety 保证规则安全：头部变量都必须在规则体中出现，
// 这样规则推导出的事实一定是 ground 的。
func validateRuleSafety(rule Rule) error {
	bound := map[string]bool{}
	for _, pred := range rule.Body {
		for _, arg := range pred.Args {
			if arg.IsVariable() {
				bound[arg.Value] = true
			}
		}
	}
	for _, arg := range rule.Head.Args {
		if arg.IsVariable() && !bound[arg.Value] {
			return fmt.Errorf("%w: variable $%s in rule head %s is not bound by the body", ErrSafety, arg.Value, rule.Head.Name)
		}
	}
	return validateBodySafety(rule.Body)
}

// validateBodySafety 保证查询体里没有悬空的常量以外问题；
// 当前子集下查询体可以包含常量与已出现的变量，变量天然在本谓词中绑定。
func validateBodySafety(body []Predicate) error {
	for _, pred := range body {
		if pred.Name == "" {
			return fmt.Errorf("%w: empty predicate name", ErrSafety)
		}
	}
	return nil
}
