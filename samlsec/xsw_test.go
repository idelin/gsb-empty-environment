package samlsec

import (
	"errors"
	"testing"
	"time"
)

// deepClone 复制测试树，避免变异污染已签名夹具。Parent 链接由 adopt 重建。
func deepClone(n *Node) *Node {
	cp := &Node{
		Kind:        n.Kind,
		Space:       n.Space,
		Local:       n.Local,
		Prefix:      n.Prefix,
		Text:        n.Text,
		Comment:     n.Comment,
		Target:      n.Target,
		Instruction: n.Instruction,
	}
	for _, a := range n.Attrs {
		cp.Attrs = append(cp.Attrs, &Attr{
			Space:       a.Space,
			Local:       a.Local,
			Prefix:      a.Prefix,
			Value:       a.Value,
			IsNamespace: a.IsNamespace,
		})
		if a.IsNamespace {
			cp.nsDecls = append(cp.nsDecls, nsBinding{prefix: a.Prefix, uri: a.Value})
		}
	}
	for _, c := range n.Children {
		adopt(cp, deepClone(c))
	}
	return cp
}

func findOnly(t *testing.T, doc *Document, space, local string) *Node {
	t.Helper()
	hits := doc.Root.FindAllDescendant(space, local)
	if len(hits) != 1 {
		t.Fatalf("expected exactly one %s, got %d", local, len(hits))
	}
	return hits[0]
}

func setID(t *testing.T, n *Node, id string) {
	t.Helper()
	for _, a := range n.Attrs {
		if !a.IsNamespace && a.Local == "ID" {
			a.Value = id
			return
		}
	}
	n.Attrs = append(n.Attrs, attr("", "ID", "", id))
}

func setText(t *testing.T, n *Node, s string) {
	t.Helper()
	if len(n.Children) == 0 {
		adopt(n, text(s))
		return
	}
	n.Children[0] = text(s)
	n.Children[0].Parent = n
}

// xswRun 执行一次 XSW 变异并要求 Validate 拒绝。
func xswRun(t *testing.T, key *testKey, now time.Time, mutate func(*Node)) {
	t.Helper()
	root := buildResponse(defaultFixture(now))
	key.sign(root.FindChild(NSSAML, "Assertion"))
	mutate(root)

	_, err := Validate([]byte(serializeXML(root)), baseOptions(key, now))
	if err == nil {
		t.Fatalf("XSW variant must be rejected")
	}
}

// TestXSW1 伪造 Assertion 被提升为 Response 直接子元素，真断言被包裹进
// Extensions。多 Assertion 结构硬校验首先拦截。
func TestXSW1_ForgedAssertionBeforeSigned(t *testing.T) {
	key := loadTestKey()
	now := fixedNow()
	xswRun(t, key, now, func(root *Node) {
		genuine := root.FindChild(NSSAML, "Assertion")
		forged := deepClone(genuine)
		setID(t, forged, "_forged-1")
		setText(t, forged.FindDescendant(NSSAML, "NameID"), "admin@evil.com")

		extensions := elem(NSSAMLP, "Extensions", "samlp")
		// 从根摘除真断言并装入 Extensions。
		removeChild(root, genuine)
		adopt(extensions, genuine)
		genuine.Parent = extensions
		insertChildAt(root, forged, 1)
		forged.Parent = root
		insertChildAt(root, extensions, 2)
		extensions.Parent = root
	})
}

// TestXSW2 与 XSW1 顺序相反：伪造断言放在已签名断言之后。
func TestXSW2_ForgeAfterSigned(t *testing.T) {
	key := loadTestKey()
	now := fixedNow()
	xswRun(t, key, now, func(root *Node) {
		genuine := root.FindChild(NSSAML, "Assertion")
		forged := deepClone(genuine)
		setID(t, forged, "_forged-2")
		setText(t, forged.FindDescendant(NSSAML, "NameID"), "admin@evil.com")

		extensions := elem(NSSAMLP, "Extensions", "samlp")
		removeChild(root, genuine)
		adopt(extensions, genuine)
		adopt(root, forged, extensions)
	})
}

// TestXSW3 已签名 Assertion 被整体塞进 Extensions，伪造断言占据直接子位置。
func TestXSW3_SignedWrappedInExtensions(t *testing.T) {
	key := loadTestKey()
	now := fixedNow()
	xswRun(t, key, now, func(root *Node) {
		genuine := root.FindChild(NSSAML, "Assertion")
		forged := deepClone(genuine)
		setID(t, forged, "_forged-3")
		setText(t, forged.FindDescendant(NSSAML, "NameID"), "root-admin")

		extensions := elem(NSSAMLP, "Extensions", "samlp")
		removeChild(root, genuine)
		adopt(extensions, genuine)
		insertChildAt(root, forged, 1)
		forged.Parent = root
		adopt(root, extensions)
	})
}

// TestXSW4 伪造 Extensions 内放带副本 ID 的断言，配合 Reference 混淆。
func TestXSW4_ExtensionForgedWithSameID(t *testing.T) {
	key := loadTestKey()
	now := fixedNow()
	xswRun(t, key, now, func(root *Node) {
		genuine := root.FindChild(NSSAML, "Assertion")
		forged := deepClone(genuine)
		setText(t, forged.FindDescendant(NSSAML, "NameID"), "admin")

		extensions := elem(NSSAMLP, "Extensions", "samlp")
		adopt(extensions, forged)
		adopt(root, extensions)
		_ = genuine
	})
}

// TestXSW5 签名被移动到伪造 Assertion 内部，真断言变成未签名兄弟。
func TestXSW5_SignatureMovedIntoForged(t *testing.T) {
	key := loadTestKey()
	now := fixedNow()
	xswRun(t, key, now, func(root *Node) {
		genuine := root.FindChild(NSSAML, "Assertion")
		sig := genuine.FindChild(NSDSIG, "Signature")
		removeChild(genuine, sig)

		forged := deepClone(genuine)
		setID(t, forged, "_forged-5")
		setText(t, forged.FindDescendant(NSSAML, "NameID"), "admin@evil.com")
		adopt(forged, sig)
		adopt(root, forged)
	})
}

// TestXSW6 签名被复制式包装：伪造断言内部嵌入 Extensions，再把签名移入。
func TestXSW6_SignatureNestedInExtensions(t *testing.T) {
	key := loadTestKey()
	now := fixedNow()
	xswRun(t, key, now, func(root *Node) {
		genuine := root.FindChild(NSSAML, "Assertion")
		sig := genuine.FindChild(NSDSIG, "Signature")
		removeChild(genuine, sig)

		forged := deepClone(genuine)
		setID(t, forged, "_forged-6")
		setText(t, forged.FindDescendant(NSSAML, "NameID"), "admin")
		extensions := elem(NSSAML, "Extensions", "saml")
		adopt(extensions, sig)
		adopt(forged, extensions)
		adopt(root, forged)
	})
}

// TestXSW7 在已签名 Response 场景下，把签名移入 Extensions。
func TestXSW7_ResponseSignatureMovedToExtensions(t *testing.T) {
	key := loadTestKey()
	now := fixedNow()
	cfg := defaultFixture(now)
	cfg.signResponse = true
	root := buildResponse(cfg)
	key.sign(root)

	sig := root.FindChild(NSDSIG, "Signature")
	removeChild(root, sig)
	extensions := elem(NSSAMLP, "Extensions", "samlp")
	adopt(extensions, sig)
	insertChildAt(root, extensions, 1)
	extensions.Parent = root

	_, err := Validate([]byte(serializeXML(root)), baseOptions(key, now))
	if !errors.Is(err, ErrSignaturePlacement) && !errors.Is(err, ErrReferenceBinding) {
		t.Fatalf("expected placement/binding rejection, got %v", err)
	}
}

// TestXSW8 Reference 被改指向伪造的兄弟断言，签名仍留在真断言上。
func TestXSW8_ReferenceRepointedToForged(t *testing.T) {
	key := loadTestKey()
	now := fixedNow()
	root := buildResponse(defaultFixture(now))
	key.sign(root.FindChild(NSSAML, "Assertion"))

	genuine := root.FindChild(NSSAML, "Assertion")
	forged := deepClone(genuine)
	setID(t, forged, "_forged-8")
	setText(t, forged.FindDescendant(NSSAML, "NameID"), "admin@evil.com")
	adopt(root, forged)

	ref := root.FindDescendant(NSDSIG, "Reference")
	for _, a := range ref.Attrs {
		if a.Local == "URI" {
			a.Value = "#_forged-8"
		}
	}

	_, err := Validate([]byte(serializeXML(root)), baseOptions(key, now))
	if err == nil {
		t.Fatalf("XSW8 repointed reference must fail")
	}
}

func removeChild(parent, child *Node) {
	for i, c := range parent.Children {
		if c == child {
			parent.Children = append(parent.Children[:i], parent.Children[i+1:]...)
			return
		}
	}
}

func insertChildAt(parent *Node, child *Node, idx int) {
	parent.Children = append(parent.Children, nil)
	copy(parent.Children[idx+1:], parent.Children[idx:])
	parent.Children[idx] = child
}
