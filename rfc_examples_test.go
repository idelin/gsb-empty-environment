package scim

import "testing"

// TestRFC7644_Examples 覆盖 RFC 7644 §3.4.2.2 中出现的代表性过滤器形态。
func TestRFC7644_Examples(t *testing.T) {
	examples := []string{
		`userName eq "bjensen"`,
		`name.familyName co "O'Malley"`,
		`userName sw "J"`,
		`title pr`,
		`meta.lastModified gt "2011-05-13T04:42:34Z"`,
		`userType eq "Employee" and (emails co "example.com" or emails.value co "example.org")`,
		`userType ne "Employee" and not (emails co "example.com" or emails.value co "example.org")`,
		`userType eq "Employee" and (emails.type eq "work")`,
		`emails[type eq "work" and value co "@example.com"]`,
		`emails[type eq "work"]`,
		`groups[value eq "481c43aa-e681-4778-8ca1-8d5e"]`,
		`urn:ietf:params:scim:schemas:core:2.0:User:userName eq "bjensen"`,
	}
	for _, raw := range examples {
		expr, err := ParseFilter(raw)
		if err != nil {
			t.Fatalf("RFC example should parse: %q: %v", raw, err)
		}
		if expr == nil || expr.Depth() < 1 {
			t.Fatalf("invalid AST for %q", raw)
		}
		// 规范化输出必须可再次解析。
		if _, err := ParseFilter(expr.String()); err != nil {
			t.Fatalf("rendered RFC example not re-parseable: %q -> %q: %v",
				raw, expr.String(), err)
		}
	}
}

func TestQuoteValue_NeutralizesInjection(t *testing.T) {
	quoted := QuoteValue(`a" or userName pr or "x`)
	expr := mustParseFilterExpr(t, `userName eq `+quoted)
	ae := expr.(*AttrExpr)
	if ae.Op != OpEq {
		t.Fatalf("quoted injection must remain eq, got %s", ae.Op)
	}
	if ae.Value.Value != `a" or userName pr or "x` {
		t.Fatalf("unexpected decoded value: %v", ae.Value.Value)
	}
}

func TestParser_ReusableInstance(t *testing.T) {
	p := NewParser(WithMaxASTDepth(4))
	if _, err := p.Parse(`userName eq "x"`); err != nil {
		t.Fatal(err)
	}
	deep := "not not not not not userName eq \"x\""
	if _, err := p.Parse(deep); err == nil {
		t.Fatal("expected depth rejection from reusable parser")
	}
}
