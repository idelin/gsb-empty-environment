package samlsec

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/asn1"
	"encoding/base64"
	"encoding/pem"
	"math/big"
	"os"
	"strings"
)

// ---- 测试用 DOM 构造器 -----------------------------------------------------

func elem(space, local, prefix string, attrs ...*Attr) *Node {
	n := &Node{Kind: KindElement, Space: space, Local: local, Prefix: prefix}
	for _, a := range attrs {
		n.Attrs = append(n.Attrs, a)
		if a.IsNamespace {
			n.nsDecls = append(n.nsDecls, nsBinding{prefix: a.Prefix, uri: a.Value})
		}
	}
	return n
}

func text(s string) *Node { return &Node{Kind: KindText, Text: s} }

func comment(s string) *Node { return &Node{Kind: KindComment, Comment: s} }

func pi(target, inst string) *Node {
	return &Node{Kind: KindProcInst, Target: target, Instruction: inst}
}

func attr(space, local, prefix, value string) *Attr {
	return &Attr{Space: space, Local: local, Prefix: prefix, Value: value}
}

func nsAttr(prefix, uri string) *Attr {
	return &Attr{Local: prefix, Prefix: prefix, Value: uri, IsNamespace: true}
}

func defaultNSAttr(uri string) *Attr {
	return &Attr{Local: "xmlns", Value: uri, IsNamespace: true}
}

// adopt 重新建立父子链接，测试构造的树无需手工维护 Parent。
func adopt(parent *Node, children ...*Node) *Node {
	for _, c := range children {
		c.Parent = parent
		parent.Children = append(parent.Children, c)
	}
	return parent
}

// serializeXML 输出结构良好的普通 XML，供 Parse 重新读入。不追求与
// c14n 字节一致，仅用于测试夹具的往返与变异。
func serializeXML(n *Node) string {
	var b strings.Builder
	writeSerial(&b, n)
	return b.String()
}

func writeSerial(b *strings.Builder, n *Node) {
	switch n.Kind {
	case KindElement:
		b.WriteByte('<')
		writeQName(b, n.Prefix, n.Local)
		for _, a := range n.Attrs {
			b.WriteByte(' ')
			if a.IsNamespace {
				if a.Prefix == "" {
					b.WriteString("xmlns=")
				} else {
					b.WriteString("xmlns:")
					b.WriteString(a.Prefix)
					b.WriteByte('=')
				}
			} else {
				writeQName(b, a.Prefix, a.Local)
				b.WriteByte('=')
			}
			b.WriteByte('"')
			b.WriteString(escapeAttr(a.Value))
			b.WriteByte('"')
		}
		if len(n.Children) == 0 {
			b.WriteString("/>")
			return
		}
		b.WriteByte('>')
		for _, c := range n.Children {
			writeSerial(b, c)
		}
		b.WriteString("</")
		writeQName(b, n.Prefix, n.Local)
		b.WriteByte('>')
	case KindText:
		b.WriteString(escapeText(n.Text))
	case KindComment:
		b.WriteString("<!--")
		b.WriteString(n.Comment)
		b.WriteString("-->")
	case KindProcInst:
		b.WriteString("<?")
		b.WriteString(n.Target)
		if n.Instruction != "" {
			b.WriteByte(' ')
			b.WriteString(n.Instruction)
		}
		b.WriteString("?>")
	}
}

func escapeText(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	return s
}

func escapeAttr(s string) string {
	s = escapeText(s)
	s = strings.ReplaceAll(s, `"`, "&quot;")
	return s
}

// ---- 测试用签名器（仅标准库） ----------------------------------------------

type testKey struct {
	rsa *rsa.PrivateKey
	ec  *ecdsa.PrivateKey
}

func loadTestKey() *testKey {
	if v := os.Getenv("SAMLSEC_TEST_KEY_PEM"); v != "" {
		block, _ := pem.Decode([]byte(v))
		parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
		if err != nil {
			panic(err)
		}
		tk := &testKey{}
		switch k := parsed.(type) {
		case *rsa.PrivateKey:
			tk.rsa = k
		case *ecdsa.PrivateKey:
			tk.ec = k
		}
		return tk
	}
	// 默认 RSA-2048；ECDSA 用例各自生成 P-256。
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		panic(err)
	}
	return &testKey{rsa: k}
}

func (k *testKey) public() crypto.PublicKey {
	if k.rsa != nil {
		return &k.rsa.PublicKey
	}
	return &k.ec.PublicKey
}

// signatureAlgo 返回当前密钥对应的签名算法 URI。
func (k *testKey) signatureAlgo() string {
	if k.rsa != nil {
		return AlgoRSASHA256
	}
	return AlgoECDSASHA256
}

// sign 对树中已占位的 ds:Signature 完成摘要与签名填充。
// parent 为承载 Signature 的元素（Response 或 Assertion）。
func (k *testKey) sign(parent *Node) {
	sig := parent.FindChild(NSDSIG, "Signature")
	if sig == nil {
		panic("test signer: Signature placeholder missing")
	}
	ref := sig.FindDescendant(NSDSIG, "Reference")
	digestValue := ref.FindChild(NSDSIG, "DigestValue")

	canonical, err := canonicalExclusive(parent, sig)
	if err != nil {
		panic(err)
	}
	digest := sha256.Sum256(canonical)
	digestValue.Children = []*Node{text(base64.StdEncoding.EncodeToString(digest[:]))}

	signedInfo := sig.FindChild(NSDSIG, "SignedInfo")
	siCanonical, err := canonicalExclusive(signedInfo, nil)
	if err != nil {
		panic(err)
	}
	siDigest := sha256.Sum256(siCanonical)

	var rawSig []byte
	if k.rsa != nil {
		rawSig, err = rsa.SignPKCS1v15(rand.Reader, k.rsa, crypto.SHA256, siDigest[:])
	} else {
		r, s, err := ecdsa.Sign(rand.Reader, k.ec, siDigest[:])
		if err != nil {
			panic(err)
		}
		rawSig, err = asn1.Marshal(struct {
			R, S *big.Int
		}{R: r, S: s})
	}
	if err != nil {
		panic(err)
	}
	valueNode := sig.FindChild(NSDSIG, "SignatureValue")
	valueNode.Children = []*Node{text(base64.StdEncoding.EncodeToString(rawSig))}
}
