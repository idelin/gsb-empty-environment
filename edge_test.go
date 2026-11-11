package deltacrl

import (
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"math/big"
	"testing"
)

// TestStatusString 覆盖三值状态的字符串表示。
func TestStatusString(t *testing.T) {
	cases := map[Status]string{
		Unknown: "unknown",
		Good:    "good",
		Revoked: "revoked",
	}
	for s, want := range cases {
		if got := s.String(); got != want {
			t.Fatalf("status %d: want %q, got %q", s, want, got)
		}
	}
}

// TestNoBaseCRLQuery 核对无任何 CRL 时查询返回 Unknown，访问器返回零值。
func TestNoBaseCRLQuery(t *testing.T) {
	ca, err := newTestCA("Edge CA")
	if err != nil {
		t.Fatal(err)
	}
	c := NewChecker(ca.cert)
	if got := c.Check(big.NewInt(1)); got.Status != Unknown {
		t.Fatalf("expected Unknown without base, got %s", got.Status)
	}
	if c.BaseCRLNumber() != nil {
		t.Fatal("expected nil base number before ingest")
	}
	if c.DeltaCount() != 0 || c.PendingCount() != 0 {
		t.Fatal("expected empty delta/pending lists")
	}
}

// TestAccessorsAfterIngest 核对 BaseCRLNumber 访问器返回当前基础序号。
func TestAccessorsAfterIngest(t *testing.T) {
	ca, err := newTestCA("Edge Accessor CA")
	if err != nil {
		t.Fatal(err)
	}
	der, err := ca.signCRL(baseSpec(77))
	if err != nil {
		t.Fatal(err)
	}
	c := newCheckerAt(t, ca)
	mustIngest(t, c, der)
	if got := c.BaseCRLNumber(); got == nil || got.Int64() != 77 {
		t.Fatalf("expected base number 77, got %v", got)
	}
}

// TestClassifyCRLErrors 直接驱动 classifyCRL，覆盖坏扩展与缺序号分支。
func TestClassifyCRLErrors(t *testing.T) {
	// delta 指示符不是合法 INTEGER（用 OCTET STRING tag 4 冒充）。
	badDelta := &x509.RevocationList{
		Number: big.NewInt(12),
		Extensions: []pkix.Extension{
			{Id: oidExtCRLNumber, Value: []byte{0x02, 0x01, 0x0c}},
			{Id: oidExtDeltaCRLIndicator, Value: []byte{0x04, 0x01, 0x05}},
		},
	}
	if _, _, _, err := classifyCRL(badDelta); err != ErrMalformedExtension {
		t.Fatalf("expected ErrMalformedExtension, got %v", err)
	}

	// cRLNumber 自身编码损坏。
	badNumber := &x509.RevocationList{
		Extensions: []pkix.Extension{
			{Id: oidExtCRLNumber, Value: []byte{0x02, 0x03, 0x01, 0x00}},
		},
	}
	if _, _, _, err := classifyCRL(badNumber); err != ErrMalformedExtension {
		t.Fatalf("expected ErrMalformedExtension, got %v", err)
	}

	// 缺少 cRLNumber。
	noNumber := &x509.RevocationList{
		Extensions: []pkix.Extension{
			{Id: oidExtDeltaCRLIndicator, Value: []byte{0x02, 0x01, 0x05}},
		},
	}
	if _, _, _, err := classifyCRL(noNumber); err != ErrMissingCRLNumber {
		t.Fatalf("expected ErrMissingCRLNumber, got %v", err)
	}

	// 正常解析：基础 CRL 与增量 CRL。
	baseRL := &x509.RevocationList{
		Number: big.NewInt(10),
		Extensions: []pkix.Extension{
			{Id: oidExtCRLNumber, Value: []byte{0x02, 0x01, 0x0a}},
		},
	}
	num, isDelta, baseRef, err := classifyCRL(baseRL)
	if err != nil || isDelta || baseRef != nil || num.Int64() != 10 {
		t.Fatalf("unexpected base classify: num=%v delta=%v ref=%v err=%v", num, isDelta, baseRef, err)
	}
}

// TestLargeCRLNumber 核对超过 int64 的 cRLNumber/baseCRLNumber 也能正确解析与比较。
func TestLargeCRLNumber(t *testing.T) {
	// 2^64 + 7，明显超出 int64 范围。
	bigBase := new(big.Int).Add(new(big.Int).Lsh(big.NewInt(1), 64), big.NewInt(7))
	bigRef := new(big.Int).Set(bigBase)
	encBase, err := asn1.Marshal(bigBase)
	if err != nil {
		t.Fatal(err)
	}
	encRef, err := asn1.Marshal(bigRef)
	if err != nil {
		t.Fatal(err)
	}
	rl := &x509.RevocationList{
		Number: bigBase,
		Extensions: []pkix.Extension{
			{Id: oidExtCRLNumber, Value: encBase},
			{Id: oidExtDeltaCRLIndicator, Value: encRef},
		},
	}
	num, isDelta, baseRef, err := classifyCRL(rl)
	if err != nil {
		t.Fatalf("classify failed: %v", err)
	}
	if !isDelta || num.Cmp(bigBase) != 0 || baseRef.Cmp(bigRef) != 0 {
		t.Fatalf("unexpected: num=%v delta=%v ref=%v", num, isDelta, baseRef)
	}
}
