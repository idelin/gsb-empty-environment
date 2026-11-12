package netsec

import (
	"crypto/sha256"
	"errors"
	"math"
	"testing"
)

// fakeVerifier 模拟证书公钥验签：合法签名定义为
// SHA-256(prevCert || signedData(node))，由测试侧的 sign 函数生成。
func fakeVerifier(cert, message, signature []byte) error {
	want := sha256.Sum256(append(append([]byte{}, cert...), message...))
	if len(signature) != sha256.Size {
		return errors.New("bad signature length")
	}
	var got [sha256.Size]byte
	copy(got[:], signature)
	if got != want {
		return errors.New("signature mismatch")
	}
	return nil
}

func sign(prevCert []byte, node *LineageNode) []byte {
	sum := sha256.Sum256(append(append([]byte{}, prevCert...), signedData(node)...))
	return sum[:]
}

func cert(name string) []byte { return []byte("DER-CERT:" + name) }

// validLineage 构造一个三节点、链签名与 SDK 区间均合法的谱系：
// [24,27] -> [28,29] -> [30, MaxInt32]，后继节点均置 0x1。
func validLineage(t *testing.T) []LineageNode {
	t.Helper()
	c0, c1, c2 := cert("old"), cert("mid"), cert("new")
	nodes := []LineageNode{
		{Certificate: c0, MinSDK: 24, MaxSDK: 27, Flags: FlagShouldLinkInLineage},
		{Certificate: c1, MinSDK: 28, MaxSDK: 29, Flags: FlagShouldLinkInLineage},
		{Certificate: c2, MinSDK: 30, MaxSDK: math.MaxInt32, Flags: FlagShouldLinkInLineage},
	}
	nodes[1].Signature = sign(c0, &nodes[1])
	nodes[2].Signature = sign(c1, &nodes[2])
	return nodes
}

func TestVerifyLineage_Valid(t *testing.T) {
	if err := VerifyLineage(validLineage(t), fakeVerifier); err != nil {
		t.Fatalf("valid lineage rejected: %v", err)
	}
}

func TestVerifyLineage_BrokenChain(t *testing.T) {
	lineage := validLineage(t)
	lineage[2].Signature = []byte("attacker-injected-signature")

	err := VerifyLineage(lineage, fakeVerifier)
	if !errors.Is(err, ErrBrokenChain) {
		t.Fatalf("want ErrBrokenChain, got %v", err)
	}
}

func TestVerifyLineage_DuplicateFingerprint(t *testing.T) {
	lineage := validLineage(t)
	lineage[2].Certificate = lineage[0].Certificate

	err := VerifyLineage(lineage, fakeVerifier)
	if !errors.Is(err, ErrDuplicateFingerprint) {
		t.Fatalf("want ErrDuplicateFingerprint, got %v", err)
	}
}

func TestVerifyLineage_OverlappingRanges(t *testing.T) {
	lineage := validLineage(t)
	// 与节点 0 的 [24,27] 重叠。
	lineage[1].MinSDK = 27
	lineage[1].Signature = sign(lineage[0].Certificate, &lineage[1])

	err := VerifyLineage(lineage, fakeVerifier)
	if !errors.Is(err, ErrSDKRangeOverlap) {
		t.Fatalf("want ErrSDKRangeOverlap, got %v", err)
	}
}

func TestVerifyLineage_RangeGap(t *testing.T) {
	lineage := validLineage(t)
	// 节点 0 收缩为 [24,26]，与节点 1 的 [28,29] 之间留下 SDK 27 的缺口。
	lineage[0].MaxSDK = 26
	lineage[2].MinSDK = 30
	lineage[2].Signature = sign(lineage[1].Certificate, &lineage[2])

	err := VerifyLineage(lineage, fakeVerifier)
	if !errors.Is(err, ErrSDKRangeGap) {
		t.Fatalf("want ErrSDKRangeGap, got %v", err)
	}
}

func TestVerifyLineage_ReversedRange(t *testing.T) {
	lineage := validLineage(t)
	lineage[1].MaxSDK = 20

	err := VerifyLineage(lineage, fakeVerifier)
	if !errors.Is(err, ErrSDKRangeReversed) {
		t.Fatalf("want ErrSDKRangeReversed, got %v", err)
	}
}

func TestVerifyLineage_StructuralErrors(t *testing.T) {
	lineage := validLineage(t)

	if err := VerifyLineage(nil, fakeVerifier); !errors.Is(err, ErrEmptyLineage) {
		t.Fatalf("want ErrEmptyLineage, got %v", err)
	}
	if err := VerifyLineage(lineage, nil); !errors.Is(err, ErrNilVerifier) {
		t.Fatalf("want ErrNilVerifier, got %v", err)
	}

	lineage[1].Certificate = nil
	if err := VerifyLineage(lineage, fakeVerifier); !errors.Is(err, ErrEmptyCertificate) {
		t.Fatalf("want ErrEmptyCertificate, got %v", err)
	}
}

func TestVerifyUpdate_HappyPath(t *testing.T) {
	lineage := validLineage(t)
	params := UpdateParams{
		Lineage:              lineage,
		InstalledCertificate: lineage[0].Certificate,
		Verifier:             fakeVerifier,
		DeviceSDK:            29,
	}
	if err := VerifyUpdate(params); err != nil {
		t.Fatalf("valid update rejected: %v", err)
	}
}

func TestVerifyUpdate_FlagMissingOnRotationPath(t *testing.T) {
	lineage := validLineage(t)
	// 已安装证书为节点 0，节点 2 未置 0x1：数据沿用必须被拒。
	lineage[2].Flags = 0
	// flags 属于被签名内容，改动后重新生成有效签名以隔离 flags 检查。
	lineage[2].Signature = sign(lineage[1].Certificate, &lineage[2])

	params := UpdateParams{
		Lineage:              lineage,
		InstalledCertificate: lineage[0].Certificate,
		Verifier:             fakeVerifier,
		DeviceSDK:            30,
	}
	if err := VerifyUpdate(params); !errors.Is(err, ErrDataRetentionNotAllowed) {
		t.Fatalf("want ErrDataRetentionNotAllowed, got %v", err)
	}
}

func TestVerifyUpdate_FlagBeforeInstalledNodeIgnored(t *testing.T) {
	lineage := validLineage(t)
	// 已安装证书为节点 1，节点 2 置位即可；节点 0 的标志位与本次更新无关。
	lineage[0].Flags = 0

	params := UpdateParams{
		Lineage:              lineage,
		InstalledCertificate: lineage[1].Certificate,
		Verifier:             fakeVerifier,
		DeviceSDK:            30,
	}
	if err := VerifyUpdate(params); err != nil {
		t.Fatalf("update from mid certificate rejected: %v", err)
	}
}

func TestVerifyUpdate_InstalledCertNotInLineage(t *testing.T) {
	lineage := validLineage(t)
	params := UpdateParams{
		Lineage:              lineage,
		InstalledCertificate: cert("attacker-appended"),
		Verifier:             fakeVerifier,
		DeviceSDK:            30,
	}
	if err := VerifyUpdate(params); !errors.Is(err, ErrInstalledCertNotInLineage) {
		t.Fatalf("want ErrInstalledCertNotInLineage, got %v", err)
	}
}

func TestVerifyUpdate_DeviceSDKNotCovered(t *testing.T) {
	lineage := validLineage(t)
	params := UpdateParams{
		Lineage:              lineage,
		InstalledCertificate: lineage[0].Certificate,
		Verifier:             fakeVerifier,
		DeviceSDK:            23,
	}
	if err := VerifyUpdate(params); !errors.Is(err, ErrDeviceSDKNotCovered) {
		t.Fatalf("want ErrDeviceSDKNotCovered, got %v", err)
	}
}

func TestCheckDowngrade(t *testing.T) {
	cases := []struct {
		name    string
		schemes SignatureSchemes
		wantErr bool
	}{
		{"v2 only is downgrade", SignatureSchemes{V2Present: true, V3Present: false}, true},
		{"v2 and v3 present", SignatureSchemes{V2Present: true, V3Present: true}, false},
		{"v3 only", SignatureSchemes{V2Present: false, V3Present: true}, false},
		{"no signature schemes", SignatureSchemes{}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := CheckDowngrade(tc.schemes)
			if tc.wantErr && !errors.Is(err, ErrDowngradedSignatureScheme) {
				t.Fatalf("want ErrDowngradedSignatureScheme, got %v", err)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestVerifyInstall_RejectsStrippedLineage(t *testing.T) {
	lineage := validLineage(t)
	params := UpdateParams{
		Lineage:              lineage,
		InstalledCertificate: lineage[0].Certificate,
		Verifier:             fakeVerifier,
		DeviceSDK:            30,
	}

	// 谱系被剥掉、仅留 v2：即使谱系本身合法也必须先按降级拒绝。
	err := VerifyInstall(params, SignatureSchemes{V2Present: true, V3Present: false})
	if !errors.Is(err, ErrDowngradedSignatureScheme) {
		t.Fatalf("want ErrDowngradedSignatureScheme, got %v", err)
	}

	if err := VerifyInstall(params, SignatureSchemes{V2Present: true, V3Present: true}); err != nil {
		t.Fatalf("v3 present should pass, got %v", err)
	}
}

func TestFingerprint_Uniqueness(t *testing.T) {
	if Fingerprint(cert("a")) == Fingerprint(cert("b")) {
		t.Fatal("distinct certificates must have distinct fingerprints")
	}
	if Fingerprint(cert("a")) != Fingerprint(cert("a")) {
		t.Fatal("identical certificates must have identical fingerprints")
	}
}
