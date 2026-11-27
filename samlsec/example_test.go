package samlsec_test

import (
	"errors"
	"fmt"
	"time"

	"formal.local/w28-2844b1dd56/samlsec"
)

// 演示宿主如何用 Options 描述信任策略并消费 VerifiedAssertion。
func ExampleOptions() {
	// 真实宿主从 IdP 元数据加载可信公钥后填入 TrustedKey。KeyInfo 内自带
	// 的密钥永远不会被信任，因此攻击者无法用自带证书通过验签。
	opts := samlsec.Options{
		ExpectedAudience:     "https://sp.example.com/",
		ExpectedRecipient:    "https://sp.example.com/acs",
		ExpectedInResponseTo: "_authn-request-1",
		ExpectedIssuer:       "https://idp.example.com/",
		ClockSkew:            30 * time.Second,
		Replay:               samlsec.NewMemoryReplayCache(time.Hour),
	}
	_ = opts

	// 验签失败时按哨兵错误分类处置。
	classify := func(err error) string {
		switch {
		case err == nil:
			return "ok"
		case errors.Is(err, samlsec.ErrReplay):
			return "replay"
		case errors.Is(err, samlsec.ErrDigestMismatch),
			errors.Is(err, samlsec.ErrSignatureValueMismatch):
			return "bad-signature"
		default:
			return "rejected"
		}
	}
	fmt.Println(classify(samlsec.ErrReplay))
	fmt.Println(classify(samlsec.ErrDigestMismatch))
	fmt.Println(classify(nil))
	// Output:
	// replay
	// bad-signature
	// ok
}

// 演示 VerifiedAssertion 暴露的业务字段全部来自验签通过的同一节点。
func ExampleVerifiedAssertion() {
	var verified *samlsec.VerifiedAssertion
	if verified == nil {
		fmt.Println("no assertion yet")
		return
	}
	fmt.Println(verified.Subject, verified.Issuer, verified.AssertionID)
	// Output:
	// no assertion yet
}
