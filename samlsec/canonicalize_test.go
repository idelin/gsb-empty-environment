package samlsec

import "testing"

func TestC14N_ExclusiveVectors(t *testing.T) {
	cases := []struct {
		name   string
		xml    string
		scope  func(*Document) *Node
		skip   func(*Node) *Node
		expect string
	}{
		{
			name: "inherited prefixes and attribute ordering",
			xml: `<samlp:Response xmlns:samlp="urn:oasis:names:tc:SAML:2.0:protocol" xmlns:saml="urn:oasis:names:tc:SAML:2.0:assertion" ID="_r1" Version="2.0">` +
				`<saml:Assertion ID="_a1" Version="2.0"><saml:NameID Format="urn:email" xml:lang="en">a&amp;b</saml:NameID></saml:Assertion></samlp:Response>`,
			scope: func(d *Document) *Node { return d.Root.FindChild(NSSAML, "Assertion") },
			expect: `<saml:Assertion xmlns:saml="urn:oasis:names:tc:SAML:2.0:assertion" ID="_a1" Version="2.0">` +
				`<saml:NameID Format="urn:email" xml:lang="en">a&amp;b</saml:NameID>` +
				`</saml:Assertion>`,
		},
		{
			name: "locally declared prefix is rendered then scoped",
			xml: `<samlp:Response xmlns:samlp="urn:oasis:names:tc:SAML:2.0:protocol" xmlns:saml="urn:oasis:names:tc:SAML:2.0:assertion" ID="_r1" Version="2.0">` +
				`<saml:Assertion xmlns:ds="http://www.w3.org/2000/09/xmldsig#" ID="_a1" Version="2.0">` +
				`<ds:Signature><ds:SignedInfo/></ds:Signature></saml:Assertion></samlp:Response>`,
			scope: func(d *Document) *Node { return d.Root.FindChild(NSSAML, "Assertion") },
			expect: `<saml:Assertion xmlns:saml="urn:oasis:names:tc:SAML:2.0:assertion" ID="_a1" Version="2.0">` +
				`<ds:Signature xmlns:ds="http://www.w3.org/2000/09/xmldsig#">` +
				`<ds:SignedInfo></ds:SignedInfo></ds:Signature></saml:Assertion>`,
		},
		{
			name: "enveloped signature subtree removed",
			xml: `<samlp:Response xmlns:samlp="urn:oasis:names:tc:SAML:2.0:protocol" xmlns:saml="urn:oasis:names:tc:SAML:2.0:assertion" xmlns:ds="http://www.w3.org/2000/09/xmldsig#" ID="_r1" Version="2.0">` +
				`<saml:Assertion ID="_a1" Version="2.0"><saml:Issuer>idp</saml:Issuer>` +
				`<ds:Signature><ds:SignedInfo/></ds:Signature></saml:Assertion></samlp:Response>`,
			scope: func(d *Document) *Node { return d.Root.FindChild(NSSAML, "Assertion") },
			skip:  func(n *Node) *Node { return n.FindChild(NSDSIG, "Signature") },
			expect: `<saml:Assertion xmlns:saml="urn:oasis:names:tc:SAML:2.0:assertion" ID="_a1" Version="2.0"><saml:Issuer>idp</saml:Issuer>` +
				`</saml:Assertion>`,
		},
		{
			name: "default namespace and special escaping",
			xml:  `<Response xmlns="urn:oasis:names:tc:SAML:2.0:protocol" ID="_r1" Note="a&#x9;b&#xA;c&#xD;d&quot;e"/>`,
			scope: func(d *Document) *Node {
				return d.Root
			},
			expect: `<Response xmlns="urn:oasis:names:tc:SAML:2.0:protocol" ID="_r1" ` +
				`Note="a&#x9;b&#xA;c&#xD;d&quot;e"></Response>`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			parsed, err := Parse([]byte(tc.xml), nil)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			target := tc.scope(parsed)
			var skip *Node
			if tc.skip != nil {
				skip = tc.skip(target)
			}
			out, err := canonicalExclusive(target, skip)
			if err != nil {
				t.Fatalf("c14n: %v", err)
			}
			if string(out) != tc.expect {
				t.Fatalf("c14n mismatch:\n got: %q\nwant: %q", string(out), tc.expect)
			}
		})
	}
}

func TestC14N_CommentsDropped(t *testing.T) {
	xml := `<samlp:Response xmlns:samlp="urn:oasis:names:tc:SAML:2.0:protocol" xmlns:saml="urn:oasis:names:tc:SAML:2.0:assertion" ID="_r1" Version="2.0"><!-- keep-me-out --><saml:Assertion ID="_a1" Version="2.0">x</saml:Assertion></samlp:Response>`
	parsed, err := Parse([]byte(xml), nil)
	if err != nil {
		t.Fatal(err)
	}
	out, err := canonicalExclusive(parsed.Root, nil)
	if err != nil {
		t.Fatal(err)
	}
	// saml 前缀虽在根声明，但 Exc-C14N 只输出可见利用的命名空间：根元素
	// 自身仅使用 samlp，saml 在 Assertion 首次利用时才渲染。
	want := `<samlp:Response xmlns:samlp="urn:oasis:names:tc:SAML:2.0:protocol" ID="_r1" Version="2.0"><saml:Assertion xmlns:saml="urn:oasis:names:tc:SAML:2.0:assertion" ID="_a1" Version="2.0">x</saml:Assertion></samlp:Response>`
	if string(out) != want {
		t.Fatalf("got %q want %q", string(out), want)
	}
}
