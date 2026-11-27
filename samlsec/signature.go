package samlsec

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/asn1"
	"encoding/base64"
	"math/big"
)

// signatureResult 记录一次验签后“可信节点”的身份，业务提取只能基于它进行。
type signatureResult struct {
	signature *Node // 唯一的 ds:Signature 节点
	signed    *Node // Reference 精确绑定并通过摘要校验的被签名元素
}

// verifySignature 在已通过结构硬校验的文档上执行完整 XML-DSig 校验。
func verifySignature(doc *Document, pub crypto.PublicKey) (*signatureResult, error) {
	sig := doc.Root.FindDescendant(NSDSIG, "Signature")
	if sig == nil {
		return nil, wrapErr("signature", ErrSignatureNotFound, "document has no ds:Signature")
	}

	// 强制路径约束：ds:Signature 只能是根 Response 或其唯一直接子
	// Assertion 的直接子元素。移入 Extensions、包装成兄弟或更深层节点
	// 的变种在此处直接失败。
	parent := sig.Parent
	if parent == nil {
		return nil, wrapErr("signature", ErrSignaturePlacement, "Signature has no parent")
	}
	if parent != doc.Root && !(parent.Parent == doc.Root && parent.Is(NSSAML, "Assertion")) {
		return nil, wrapErr("signature", ErrSignaturePlacement,
			"Signature at illegal path %s", sig.Path())
	}

	signedInfo := sig.FindChild(NSDSIG, "SignedInfo")
	if signedInfo == nil {
		return nil, wrapErr("signature", ErrRequiredField, "SignedInfo missing")
	}
	if err := checkAlgo(signedInfo.FindChild(NSDSIG, "CanonicalizationMethod"), AlgoExcC14N); err != nil {
		return nil, err
	}
	sigMethod := signedInfo.FindChild(NSDSIG, "SignatureMethod")
	if sigMethod == nil {
		return nil, wrapErr("signature", ErrRequiredField, "SignatureMethod missing")
	}
	sigAlgo, _ := sigMethod.AttrValue("", "Algorithm")
	switch sigAlgo {
	case AlgoRSASHA256, AlgoRSAPSSSHA256, AlgoECDSASHA256:
	default:
		return nil, wrapErr("signature", ErrUnsupportedAlgorithm,
			"SignatureMethod %q not allowed", sigAlgo)
	}

	refs := signedInfo.ChildElements(NSDSIG, "Reference")
	if len(refs) != 1 {
		return nil, wrapErr("signature", ErrReferenceBinding,
			"exactly one Reference required, got %d", len(refs))
	}
	ref := refs[0]
	uri, _ := ref.AttrValue("", "URI")
	if len(uri) < 2 || uri[0] != '#' {
		return nil, wrapErr("signature", ErrReferenceBinding,
			"Reference URI %q must be a same-document fragment", uri)
	}
	fragment := uri[1:]
	// 只允许裸 ID 片段：禁止空片段（覆盖整文档语义）、xpointer()/scheme
	// 表达式以及任何含空白或嵌套分隔符的形式，杜绝多目标/指针型绕过。
	if fragment == "" || !isBareID(fragment) {
		return nil, wrapErr("signature", ErrReferenceBinding,
			"Reference URI %q must be a bare #id fragment", uri)
	}
	target, ok := doc.idIndex[fragment]
	if !ok || target == nil {
		return nil, wrapErr("signature", ErrReferenceBinding,
			"Reference URI %q does not resolve to a node", uri)
	}

	// 精确单目标绑定：被签名元素必须是根 Response 本身，或其唯一直接子
	// Assertion。任何 Extensions 内节点、伪造兄弟 Assertion 都无法满足。
	if target != doc.Root && !(target.Parent == doc.Root && target.Is(NSSAML, "Assertion")) {
		return nil, wrapErr("signature", ErrReferenceBinding,
			"signed target path %s is outside the allowed set", target.Path())
	}
	// 被签名元素必须就是承载该 Signature 的元素，杜绝“签名 A 元素、
	// 业务读取 B 元素”的包装结构。
	if target != parent {
		return nil, wrapErr("signature", ErrReferenceBinding,
			"Reference binds %s but Signature is carried by %s",
			target.Path(), parent.Path())
	}

	if err := verifyReference(ref, target, sig); err != nil {
		return nil, err
	}
	if err := verifySignatureValue(sig, signedInfo, sigAlgo, pub); err != nil {
		return nil, err
	}
	return &signatureResult{signature: sig, signed: target}, nil
}

func checkAlgo(node *Node, want string) error {
	if node == nil {
		return wrapErr("signature", ErrRequiredField, "algorithm element missing")
	}
	got, _ := node.AttrValue("", "Algorithm")
	if got != want {
		return wrapErr("signature", ErrUnsupportedAlgorithm,
			"algorithm %q not allowed, require %q", got, want)
	}
	return nil
}

func verifyReference(ref, target, sig *Node) error {
	digestMethod := ref.FindChild(NSDSIG, "DigestMethod")
	if err := checkAlgo(digestMethod, AlgoSHA256); err != nil {
		return err
	}
	transforms := ref.FindChild(NSDSIG, "Transforms")
	if transforms == nil || len(transforms.ChildElements(NSDSIG, "Transform")) == 0 {
		return wrapErr("signature", ErrUnsupportedAlgorithm, "Transforms missing")
	}
	gotEnveloped, gotExc := false, false
	for _, tr := range transforms.ChildElements(NSDSIG, "Transform") {
		algo, _ := tr.AttrValue("", "Algorithm")
		switch algo {
		case AlgoEnveloped:
			gotEnveloped = true
		case AlgoExcC14N:
			gotExc = true
		default:
			return wrapErr("signature", ErrUnsupportedAlgorithm,
				"transform %q not allowed", algo)
		}
	}
	if !gotEnveloped || !gotExc {
		return wrapErr("signature", ErrUnsupportedAlgorithm,
			"transform chain must contain enveloped-signature and exc-c14n")
	}

	canonical, err := canonicalExclusive(target, sig)
	if err != nil {
		return wrapErr("signature", ErrDigestMismatch, "canonicalization failed: %v", err)
	}
	sum := sha256.Sum256(canonical)
	gotDigest := base64.StdEncoding.EncodeToString(sum[:])

	digestValueNode := ref.FindChild(NSDSIG, "DigestValue")
	if digestValueNode == nil {
		return wrapErr("signature", ErrRequiredField, "DigestValue missing")
	}
	wantDigest := digestValueNode.TextContent()
	if !constantTimeEqual(gotDigest, wantDigest) {
		return wrapErr("signature", ErrDigestMismatch,
			"digest mismatch for %s", target.Path())
	}
	return nil
}

func verifySignatureValue(sig, signedInfo *Node, algo string, pub crypto.PublicKey) error {
	valueNode := sig.FindChild(NSDSIG, "SignatureValue")
	if valueNode == nil {
		return wrapErr("signature", ErrRequiredField, "SignatureValue missing")
	}
	rawSig, err := base64.StdEncoding.DecodeString(valueNode.TextContent())
	if err != nil {
		return wrapErr("signature", ErrSignatureValueMismatch,
			"SignatureValue is not base64: %v", err)
	}
	canonical, err := canonicalExclusive(signedInfo, nil)
	if err != nil {
		return wrapErr("signature", ErrSignatureValueMismatch,
			"SignedInfo canonicalization failed: %v", err)
	}
	sum := sha256.Sum256(canonical)

	switch key := pub.(type) {
	case *rsa.PublicKey:
		switch algo {
		case AlgoRSASHA256:
			err = rsa.VerifyPKCS1v15(key, crypto.SHA256, sum[:], rawSig)
		case AlgoRSAPSSSHA256:
			err = rsa.VerifyPSS(key, crypto.SHA256, sum[:], rawSig, nil)
		default:
			return wrapErr("signature", ErrUnsupportedAlgorithm,
				"RSA key cannot use %q", algo)
		}
	case *ecdsa.PublicKey:
		if algo != AlgoECDSASHA256 {
			return wrapErr("signature", ErrUnsupportedAlgorithm,
				"ECDSA key cannot use %q", algo)
		}
		type ecdsaSig struct {
			R, S *big.Int
		}
		var es ecdsaSig
		if rest, derr := asn1.Unmarshal(rawSig, &es); derr != nil || len(rest) != 0 ||
			es.R == nil || es.S == nil || !ecdsa.Verify(key, sum[:], es.R, es.S) {
			err = rsa.ErrVerification
		}
	default:
		return wrapErr("signature", ErrUnsupportedAlgorithm,
			"unsupported public key type %T", pub)
	}
	if err != nil {
		return wrapErr("signature", ErrSignatureValueMismatch, "%v", err)
	}
	return nil
}

func constantTimeEqual(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	var v byte
	for i := 0; i < len(a); i++ {
		v |= a[i] ^ b[i]
	}
	return v == 0
}

// isBareID 限定 Reference 片段为单个 XML ID：仅允许字母、数字以及 XML
// Name 中常见的安全标点，禁止空白、#、括号（xpointer）等结构性字符。
func isBareID(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		case c == '_' || c == '-' || c == '.' || c == ':':
		default:
			return false
		}
	}
	return true
}
