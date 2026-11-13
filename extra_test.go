package netsec

import (
	"errors"
	"math/big"
	"strings"
	"testing"
)

// TestItemErrorWrapping 验证条目错误的文本与 Unwrap 链。
func TestItemErrorWrapping(t *testing.T) {
	err := &ItemError{Index: 7, Reason: errScalarRange}
	if !strings.Contains(err.Error(), "item 7") {
		t.Fatalf("error text = %q", err.Error())
	}
	if !errors.Is(err, errScalarRange) {
		t.Fatal("Unwrap chain must expose the underlying reason")
	}
}

// TestSignRejectsBadPrivateKey 覆盖签名入口的私钥校验。
func TestSignRejectsBadPrivateKey(t *testing.T) {
	if _, _, err := Sign(make([]byte, 32), []byte("msg"), nil); err == nil {
		t.Fatal("zero private key must be rejected")
	}
	if _, _, err := Sign([]byte{1, 2, 3}, nil, nil); err == nil {
		t.Fatal("short private key must be rejected")
	}
}

// TestPointAccumulatorInfinity 直接覆盖无穷远点加法与相等判定分支。
func TestPointAccumulatorInfinity(t *testing.T) {
	_, _, pubErr := decodePoint(make([]byte, 64))
	if pubErr == nil {
		t.Fatal("64-byte input must be rejected")
	}

	// nil 表示无穷远点 O
	x, y := curve.ScalarBaseMult(encodeScalar(big.NewInt(3)))
	ox, oy := addPoint(nil, nil, x, y)
	if ox == nil || ox.Cmp(x) != 0 || oy.Cmp(y) != 0 {
		t.Fatal("O + P must equal P")
	}
	ox, oy = addPoint(x, y, nil, nil)
	if ox == nil || ox.Cmp(x) != 0 {
		t.Fatal("P + O must equal P")
	}
	if !pointsEqual(nil, nil, nil, nil) {
		t.Fatal("O == O must hold")
	}
	if pointsEqual(nil, nil, x, y) || pointsEqual(x, y, nil, nil) {
		t.Fatal("O must not equal a finite point")
	}

	// P + (-P) = O
	negY := new(big.Int).Sub(curve.Params().P, y)
	infX, infY := addPoint(x, y, x, negY)
	if !isInfinity(infX, infY) {
		t.Fatalf("P + (-P) must be the point at infinity, got (%v,%v)", infX, infY)
	}
	if !pointsEqual(infX, infY, nil, nil) {
		t.Fatal("normalized infinity must compare equal to nil representation")
	}
}

// TestLocateInconsistentFallback 人为构造“父失败而两子均通过”的不一致，
// 验证 locateInvalid 上报 inconsistent，使 VerifyBatch 整批退回逐份验证。
func TestLocateInconsistentFallback(t *testing.T) {
	const n = 4
	raw := make([]Item, n)
	for i := 0; i < n; i++ {
		priv, pub, _ := GenerateKey(nil)
		raw[i] = mustSign(t, priv, pub, []byte{byte(i)})
	}
	// 让索引 1 失效：真实整批等式必然失败，逐份参考定位到 [1]。
	raw[1].S = addQToScalar(raw[1].S)
	items := parseForTest(t, raw)
	coeffs := onesCoeffs(n)

	// 真实系数下应精确定位到索引 1。
	bad, inconsistent := locateInvalid(items, coeffs)
	if inconsistent {
		t.Fatal("honest coefficients should not produce an inconsistency")
	}
	if !equalInts(bad, []int{1}) {
		t.Fatalf("honest locate invalid=%v, want [1]", bad)
	}

	// 注入矛盾：任何多元素子集查询都声称通过，而整批已确认失败，
	// 于是第一层二分出现“两子均通过”，必须上报 inconsistent。
	original := subsetCheck
	subsetCheck = func(_ []parsedItem, _ []*big.Int, idx []int) bool {
		return len(idx) > 1
	}
	defer func() { subsetCheck = original }()

	if _, inconsistent := locateInvalid(items, coeffs); !inconsistent {
		t.Fatal("parent-fail/children-pass must be reported as inconsistent")
	}

	// 端到端：同样的桩作用于 VerifyBatch 时应退回逐份验证，结果仍精确。
	res, err := VerifyBatch(raw, deterministicSeed{value: seedFromByte(0x99)})
	if err != nil {
		t.Fatal(err)
	}
	if res.Method != MethodSingleFallback {
		t.Fatalf("inconsistency must trigger per-item fallback, got method=%s", res.Method)
	}
	if !equalInts(res.Invalid, referenceInvalid(t, raw)) {
		t.Fatalf("fallback invalid=%v want=%v", res.Invalid, referenceInvalid(t, raw))
	}
}

func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
