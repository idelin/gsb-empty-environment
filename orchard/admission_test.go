package orchard

import (
	"errors"
	"math/big"
	"testing"
)

// leBytes 把整数转成 32 字节小端编码，便于构造测试向量。
func leBytes(n *big.Int) [32]byte {
	var out [32]byte
	fill := n.FillBytes(out[:]) // FillBytes 使用大端序，结果写回 out 并返回同一底层数组
	// 反转为小端序。
	for i, j := 0, len(fill)-1; i < j; i, j = i+1, j-1 {
		fill[i], fill[j] = fill[j], fill[i]
	}
	return out
}

func modulusBig() *big.Int {
	m, ok := new(big.Int).SetString(
		"40000000000000000000000000000000224698fc094cf91b992d30ed00000001", 16)
	if !ok {
		panic("invalid Pallas modulus hex constant")
	}
	return m
}

// TestModulusConstant 用 math/big 交叉验证手写的小端模数常量，
// 防止常量字节序或取值抄写出错。
func TestModulusConstant(t *testing.T) {
	le := append([]byte(nil), PallasBaseFieldModulus[:]...)
	for i, j := 0, len(le)-1; i < j; i, j = i+1, j-1 {
		le[i], le[j] = le[j], le[i]
	}
	got := new(big.Int).SetBytes(le)
	want := modulusBig()
	if got.Cmp(want) != 0 {
		t.Fatalf("modulus constant = %x, want %x", got, want)
	}
}

func TestIsCanonicalFieldElement(t *testing.T) {
	mod := modulusBig()
	cases := []struct {
		name string
		val  *big.Int
		want bool
	}{
		{"zero", big.NewInt(0), true},
		{"one", big.NewInt(1), true},
		{"modulus minus one", new(big.Int).Sub(mod, big.NewInt(1)), true},
		{"equal to modulus", new(big.Int).Set(mod), false},
		{"modulus plus one", new(big.Int).Add(mod, big.NewInt(1)), false},
		{"max 256-bit", new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 256), big.NewInt(1)), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isCanonicalFieldElement(leBytes(tc.val)); got != tc.want {
				t.Fatalf("isCanonicalFieldElement(%s) = %v, want %v", tc.name, got, tc.want)
			}
		})
	}
}

// validBundle 构造一个结构合法的束：规范域元素、已知锚点、低两位 flags。
func validBundle(t *testing.T) *Bundle {
	t.Helper()
	return &Bundle{
		Flags:  0x03,
		Anchor: leBytes(big.NewInt(7)),
		Actions: []Action{
			{
				Nullifier:  leBytes(big.NewInt(101)),
				Commitment: leBytes(big.NewInt(202)),
			},
			{
				Nullifier:  leBytes(big.NewInt(102)),
				Commitment: leBytes(big.NewInt(203)),
			},
		},
	}
}

func newTestValidator(t *testing.T, spent SpentNullifiers, proof ProofVerifier, signature SignatureVerifier) *BlockValidator {
	t.Helper()
	roots := RootSet{
		leBytes(big.NewInt(7)): {},
	}
	v, err := NewBlockValidator(roots, spent, proof, signature)
	if err != nil {
		t.Fatalf("NewBlockValidator: %v", err)
	}
	return v
}

func noopProof(*Bundle) error        { return nil }
func noopSignature(*Bundle) error    { return nil }
func failingProof(*Bundle) error     { return errors.New("bad proof") }
func failingSignature(*Bundle) error { return errors.New("bad signature") }

func TestAdmitBundleAcceptsValidBundle(t *testing.T) {
	v := newTestValidator(t, nil, noopProof, noopSignature)
	if err := v.AdmitBundle(validBundle(t)); err != nil {
		t.Fatalf("valid bundle rejected: %v", err)
	}

	// flags 的四种合法取值（保留位全为零）都应通过结构检查。
	for _, flags := range []byte{0x00, 0x01, 0x02, 0x03} {
		v := newTestValidator(t, nil, noopProof, noopSignature)
		bundle := validBundle(t)
		bundle.Flags = flags
		if err := v.AdmitBundle(bundle); err != nil {
			t.Fatalf("flags=0x%02x rejected: %v", flags, err)
		}
	}
}

func TestRejectsReservedFlags(t *testing.T) {
	reserved := []byte{0x04, 0x08, 0x10, 0x80, 0xfc, 0xff}
	for _, flags := range reserved {
		v := newTestValidator(t, nil, noopProof, noopSignature)
		bundle := validBundle(t)
		bundle.Flags = flags
		err := v.AdmitBundle(bundle)
		if !errors.Is(err, ErrReservedFlags) {
			t.Fatalf("flags=0x%02x: got %v, want ErrReservedFlags", flags, err)
		}
	}
}

func TestRejectsEncodingEqualToModulus(t *testing.T) {
	mod := leBytes(modulusBig())

	t.Run("nullifier equals modulus", func(t *testing.T) {
		v := newTestValidator(t, nil, noopProof, noopSignature)
		bundle := validBundle(t)
		bundle.Actions[0].Nullifier = mod
		if !errors.Is(v.AdmitBundle(bundle), ErrNonCanonicalFieldElement) {
			t.Fatalf("equal-modulus nullifier not rejected")
		}
	})

	t.Run("cmx equals modulus", func(t *testing.T) {
		v := newTestValidator(t, nil, noopProof, noopSignature)
		bundle := validBundle(t)
		bundle.Actions[1].Commitment = mod
		if !errors.Is(v.AdmitBundle(bundle), ErrNonCanonicalFieldElement) {
			t.Fatalf("equal-modulus cmx not rejected")
		}
	})

	t.Run("value above modulus", func(t *testing.T) {
		v := newTestValidator(t, nil, noopProof, noopSignature)
		bundle := validBundle(t)
		bundle.Actions[0].Nullifier = leBytes(new(big.Int).Add(modulusBig(), big.NewInt(1)))
		if !errors.Is(v.AdmitBundle(bundle), ErrNonCanonicalFieldElement) {
			t.Fatalf("above-modulus nullifier not rejected")
		}
	})
}

func TestRejectsDuplicateNullifierWithinBundle(t *testing.T) {
	v := newTestValidator(t, nil, noopProof, noopSignature)
	bundle := validBundle(t)
	bundle.Actions[1].Nullifier = bundle.Actions[0].Nullifier
	if !errors.Is(v.AdmitBundle(bundle), ErrDuplicateNullifier) {
		t.Fatalf("in-bundle duplicate nullifier not rejected")
	}
}

func TestRejectsSpentNullifier(t *testing.T) {
	bundle := validBundle(t)
	spent := NullifierSet{bundle.Actions[0].Nullifier: {}}
	v := newTestValidator(t, spent, noopProof, noopSignature)
	if !errors.Is(v.AdmitBundle(bundle), ErrSpentNullifier) {
		t.Fatalf("on-chain spent nullifier not rejected")
	}
}

func TestRejectsUnknownAnchor(t *testing.T) {
	v := newTestValidator(t, nil, noopProof, noopSignature)
	bundle := validBundle(t)
	bundle.Anchor = leBytes(big.NewInt(999)) // 规范但不在已知树根集合中
	if !errors.Is(v.AdmitBundle(bundle), ErrUnknownAnchor) {
		t.Fatalf("unknown anchor not rejected")
	}
}

func TestRejectsNonCanonicalAnchor(t *testing.T) {
	v := newTestValidator(t, nil, noopProof, noopSignature)
	bundle := validBundle(t)
	bundle.Anchor = leBytes(modulusBig())
	err := v.AdmitBundle(bundle)
	if !errors.Is(err, ErrNonCanonicalFieldElement) {
		t.Fatalf("non-canonical anchor: got %v, want ErrNonCanonicalFieldElement", err)
	}
}

func TestRejectsDuplicateNullifierAcrossBundles(t *testing.T) {
	v := newTestValidator(t, nil, noopProof, noopSignature)

	first := validBundle(t)
	if err := v.AdmitBundle(first); err != nil {
		t.Fatalf("first bundle: %v", err)
	}

	second := validBundle(t)
	// 与第一个束共享一个废止符，必须在区块级别被拒绝。
	second.Actions[0].Nullifier = first.Actions[1].Nullifier
	if !errors.Is(v.AdmitBundle(second), ErrDuplicateNullifier) {
		t.Fatalf("cross-bundle duplicate nullifier not rejected")
	}
}

func TestRejectedBundleDoesNotRegisterNullifiers(t *testing.T) {
	v := newTestValidator(t, nil, noopProof, noopSignature)

	bad := validBundle(t)
	bad.Flags = 0x04 // 保留位非零，应被拒绝
	if err := v.AdmitBundle(bad); err == nil {
		t.Fatal("expected rejection")
	}

	// 被拒绝束的废止符不得污染区块去重状态：同样的束修正 flags 后应通过。
	fixed := validBundle(t)
	if err := v.AdmitBundle(fixed); err != nil {
		t.Fatalf("nullifiers from rejected bundle leaked into block state: %v", err)
	}
}

func TestInjectedVerifiers(t *testing.T) {
	t.Run("proof failure short-circuits", func(t *testing.T) {
		sigCalled := false
		signature := func(*Bundle) error {
			sigCalled = true
			return nil
		}
		v := newTestValidator(t, nil, failingProof, signature)
		if err := v.AdmitBundle(validBundle(t)); err == nil {
			t.Fatal("proof failure not propagated")
		}
		if sigCalled {
			t.Fatal("signature verifier must not run after proof failure")
		}
	})

	t.Run("signature failure propagated", func(t *testing.T) {
		v := newTestValidator(t, nil, noopProof, failingSignature)
		if err := v.AdmitBundle(validBundle(t)); err == nil {
			t.Fatal("signature failure not propagated")
		}
	})

	t.Run("nil verifiers skip cryptographic checks", func(t *testing.T) {
		v := newTestValidator(t, nil, nil, nil)
		if err := v.AdmitBundle(validBundle(t)); err != nil {
			t.Fatalf("nil verifiers should skip checks: %v", err)
		}
	})
}

func TestEmptyAndNilBundle(t *testing.T) {
	v := newTestValidator(t, nil, nil, nil)
	if !errors.Is(v.AdmitBundle(&Bundle{}), ErrEmptyBundle) {
		t.Fatal("empty bundle not rejected")
	}
	if err := v.AdmitBundle(nil); err == nil {
		t.Fatal("nil bundle not rejected")
	}
}

func TestNewBlockValidatorRequiresRoots(t *testing.T) {
	if _, err := NewBlockValidator(nil, nil, nil, nil); err == nil {
		t.Fatal("nil KnownRoots should be rejected")
	}
}

func TestValidateBundleStandalone(t *testing.T) {
	roots := RootSet{leBytes(big.NewInt(7)): {}}
	if err := ValidateBundle(validBundle(t), roots, nil, noopProof, noopSignature); err != nil {
		t.Fatalf("ValidateBundle: %v", err)
	}

	bundle := validBundle(t)
	bundle.Actions[0].Nullifier = leBytes(modulusBig())
	if !errors.Is(ValidateBundle(bundle, roots, nil, noopProof, noopSignature), ErrNonCanonicalFieldElement) {
		t.Fatal("standalone validation did not reject non-canonical nullifier")
	}
}
