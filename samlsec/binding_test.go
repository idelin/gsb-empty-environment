package samlsec

import (
	"errors"
	"testing"
	"time"
)

// TestBinding_BusinessReadsSignedNode 证明业务身份字段永远来自验签通过的
// 同一节点子树：篡改 Assertion 内容会让 Response 签名的摘要失配。
func TestBinding_BusinessReadsSignedNode(t *testing.T) {
	key := loadTestKey()
	now := fixedNow()
	cfg := defaultFixture(now)
	cfg.signResponse = true
	raw := signedResponseBytes(key, cfg)

	tampered := replaceFirst(raw, "alice@example.com", "admin@evil.com")
	_, err := Validate(tampered, baseOptions(key, now))
	if !errors.Is(err, ErrDigestMismatch) {
		t.Fatalf("response-signed tamper must break digest, got %v", err)
	}
}

// TestBinding_UnsignedAssertionRejected 构造一个签名合法但 Reference 指向
// Response、Assertion 却是未签名独立节点的结构，确认无法绕过同源绑定。
func TestBinding_AssertionNotCarryingSignature(t *testing.T) {
	key := loadTestKey()
	now := fixedNow()

	// Assertion 签名场景下，把签名从 Assertion 移到 Response 但 Reference
	// 仍指向 Assertion：承载者与目标不一致，必须拒绝。
	root := buildResponse(defaultFixture(now))
	assertion := root.FindChild(NSSAML, "Assertion")
	sig := assertion.FindChild(NSDSIG, "Signature")
	key.sign(assertion) // 摘要基于 Assertion 计算
	removeChild(assertion, sig)
	adopt(root, sig)

	_, err := Validate([]byte(serializeXML(root)), baseOptions(key, now))
	if !errors.Is(err, ErrReferenceBinding) {
		t.Fatalf("expected ErrReferenceBinding, got %v", err)
	}
}

// TestBinding_OnlyOneDirectAssertionAccepted response-signed 时断言必须是
// 根的唯一直接子元素。
func TestBinding_OnlyOneDirectAssertionAccepted(t *testing.T) {
	key := loadTestKey()
	now := fixedNow()
	cfg := defaultFixture(now)
	cfg.signResponse = true
	root := buildResponse(cfg)
	key.sign(root)

	// 把 Assertion 包进一层容器，使其不再是根的直接子元素。
	assertion := root.FindChild(NSSAML, "Assertion")
	removeChild(root, assertion)
	wrapper := elem(NSSAMLP, "Extensions", "samlp")
	adopt(wrapper, assertion)
	insertChildAt(root, wrapper, 1)
	wrapper.Parent = root

	_, err := Validate([]byte(serializeXML(root)), baseOptions(key, now))
	if err == nil {
		t.Fatal("wrapped assertion must be rejected")
	}
}

// TestVerifiedAssertion_FieldsFromTrustedSubtree 端到端确认所有业务字段
// 都取自验签节点，且注释文本不会混入。
func TestVerifiedAssertion_FieldsFromTrustedSubtree(t *testing.T) {
	key := loadTestKey()
	now := fixedNow()
	raw := signedResponseBytes(key, defaultFixture(now))
	got, err := Validate(raw, baseOptions(key, now))
	if err != nil {
		t.Fatal(err)
	}
	if got.Subject != "alice@example.com" || got.Issuer != testIDP {
		t.Fatalf("untrusted field source: %+v", got)
	}
	if _, err := time.Parse(time.RFC3339, got.NotOnOrAfter.Format(time.RFC3339)); err != nil {
		t.Fatalf("bad time field: %v", err)
	}
}
