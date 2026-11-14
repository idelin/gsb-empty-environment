package depverify

import "strings"

func (p *parser) parseRequire(out *GoModFile, line int) error {
	if t := p.peek(); t != nil && t.kind == tokenPunct && t.text == "(" {
		p.next()
		for {
			p.skipNewlines()
			cur := p.peek()
			if cur != nil && cur.kind == tokenPunct && cur.text == ")" {
				p.next()
				return nil
			}
			if cur == nil {
				return &ParseError{Line: line, Msg: "unterminated require block"}
			}
			if err := p.parseOneRequire(out, true); err != nil {
				return err
			}
		}
	}
	return p.parseOneRequire(out, false)
}

func (p *parser) parseOneRequire(out *GoModFile, block bool) error {
	path, line, err := p.valueToken("module path")
	if err != nil {
		return err
	}
	version, _, err := p.valueToken("version")
	if err != nil {
		return err
	}
	if !validVersion(version) {
		return &ParseError{Line: line, Msg: "invalid version " + version}
	}
	req := Requirement{Path: path, Version: version, Line: line}
	for !p.atBlockEnd(block) {
		t := p.next()
		if t.kind == tokenComment {
			for _, f := range strings.Fields(t.text) {
				if f == "indirect" {
					req.Indirect = true
				}
			}
		}
		if t.kind == tokenWord && t.text == "indirect" {
			req.Indirect = true
		}
	}
	out.Requires = append(out.Requires, req)
	return p.statementEnd(block)
}

func (p *parser) parseExclude(out *GoModFile, line int) error {
	if t := p.peek(); t != nil && t.kind == tokenPunct && t.text == "(" {
		p.next()
		for {
			p.skipNewlines()
			cur := p.peek()
			if cur != nil && cur.kind == tokenPunct && cur.text == ")" {
				p.next()
				return nil
			}
			if cur == nil {
				return &ParseError{Line: line, Msg: "unterminated exclude block"}
			}
			if err := p.parseOneExclude(out, true); err != nil {
				return err
			}
		}
	}
	return p.parseOneExclude(out, false)
}

func (p *parser) parseOneExclude(out *GoModFile, block bool) error {
	path, line, err := p.valueToken("module path")
	if err != nil {
		return err
	}
	version, _, err := p.valueToken("version")
	if err != nil {
		return err
	}
	if !validVersion(version) {
		return &ParseError{Line: line, Msg: "invalid version " + version}
	}
	out.Excludes = append(out.Excludes, Exclusion{Path: path, Version: version, Line: line})
	return p.statementEnd(block)
}

func (p *parser) parseReplace(out *GoModFile, line int) error {
	if t := p.peek(); t != nil && t.kind == tokenPunct && t.text == "(" {
		p.next()
		for {
			p.skipNewlines()
			cur := p.peek()
			if cur != nil && cur.kind == tokenPunct && cur.text == ")" {
				p.next()
				return nil
			}
			if cur == nil {
				return &ParseError{Line: line, Msg: "unterminated replace block"}
			}
			if err := p.parseOneReplace(out, true); err != nil {
				return err
			}
		}
	}
	return p.parseOneReplace(out, false)
}

func (p *parser) parseOneReplace(out *GoModFile, block bool) error {
	oldPath, line, err := p.valueToken("replaced module path")
	if err != nil {
		return err
	}
	rep := Replacement{OldPath: oldPath, Line: line}

	if t := p.peek(); t != nil && t.kind != tokenPunct {
		version, vline, err := p.valueToken("version")
		if err != nil {
			return err
		}
		if !validVersion(version) {
			return &ParseError{Line: vline, Msg: "invalid version " + version}
		}
		rep.OldVersion = version
	} else {
		rep.Wildcard = true
	}

	if err := p.expectArrow(); err != nil {
		return err
	}

	newPath, _, err := p.valueToken("replacement path")
	if err != nil {
		return err
	}
	rep.NewPath = newPath

	if p.atBlockEnd(block) {
		rep.Local = true
		out.Replacements = append(out.Replacements, rep)
		return p.statementEnd(block)
	}

	newVersion, vline, err := p.valueToken("replacement version")
	if err != nil {
		return err
	}
	if !validVersion(newVersion) {
		return &ParseError{Line: vline, Msg: "invalid version " + newVersion}
	}
	rep.NewVersion = newVersion

	for !p.atBlockEnd(block) {
		p.next()
	}
	out.Replacements = append(out.Replacements, rep)
	return p.statementEnd(block)
}
