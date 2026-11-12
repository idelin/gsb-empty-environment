package taskpkg

import (
	"math/big"
	"testing"
)

func ratEqual(t *testing.T, got *big.Rat, want string) {
	t.Helper()
	if got == nil {
		t.Fatalf("期望 %s，得到 nil", want)
	}
	w, ok := new(big.Rat).SetString(want)
	if !ok {
		t.Fatalf("测试常量非法: %q", want)
	}
	if got.Cmp(w) != 0 {
		t.Fatalf("期望 %s，得到 %s", want, got.RatString())
	}
}

func TestParseDecimalThousandsVsCommaDecimal(t *testing.T) {
	// "1,234" 的两种解释：千分位 1234 vs 欧陆小数 1.234。
	v, err := ParseDecimal("1,234", '.', GroupComma)
	if err != nil {
		t.Fatal(err)
	}
	ratEqual(t, v, "1234")

	v, err = ParseDecimal("1,234", ',', GroupNone)
	if err != nil {
		t.Fatal(err)
	}
	ratEqual(t, v, "617/500")

	// 点分组 + 逗号小数：1.234,56 -> 1234.56
	v, err = ParseDecimal("1.234,56", ',', GroupDot)
	if err != nil {
		t.Fatal(err)
	}
	ratEqual(t, v, "30864/25")
}

func TestParseDecimalFractionsAndSigns(t *testing.T) {
	cases := []struct {
		raw     string
		decimal byte
		group   int
		want    string
	}{
		{"0.5", '.', GroupNone, "1/2"},
		{".5", '.', GroupNone, "1/2"},
		{"-,5", ',', GroupNone, "-1/2"},
		{"+1,000.25", '.', GroupComma, "4001/4"},
		{"-2.5", '.', GroupNone, "-5/2"},
		{"1 234 567", '.', GroupSpace, "1234567"},
		{"1\u00a0234\u00a0567,89", ',', GroupSpace, "123456789/100"},
	}
	for _, tc := range cases {
		got, err := ParseDecimal(tc.raw, tc.decimal, tc.group)
		if err != nil {
			t.Fatalf("%q 解析失败: %v", tc.raw, err)
		}
		ratEqual(t, got, tc.want)
	}
}

func TestParseDecimalMissingAndMalformed(t *testing.T) {
	if v, err := ParseDecimal("   ", '.', GroupNone); err != nil || v != nil {
		t.Fatalf("空白应解析为缺失 (nil,nil)，得到 %v, %v", v, err)
	}
	bad := []struct {
		raw     string
		decimal byte
		group   int
	}{
		{"12,34", '.', GroupComma},    // 千分位组不足 3 位
		{"1,234,5", '.', GroupComma},  // 末组不是 3 位
		{"1.234.56", '.', GroupComma}, // 分组符与小数符冲突/重复小数符
		{"1,2,3", ',', GroupNone},     // 无分组却出现逗号且逗号是小数符：重复小数符
		{"12,", '.', GroupComma},      // 末尾空分组
		{",123", '.', GroupComma},     // 开头空分组
		{"abc", '.', GroupNone},       // 非数字
		{"-", '.', GroupNone},         // 只有符号
		{"1 23", '.', GroupSpace},     // 空格分组不足 3 位
		{"1.2.3", '.', GroupNone},     // 小数符多次
	}
	for _, tc := range bad {
		if _, err := ParseDecimal(tc.raw, tc.decimal, tc.group); err == nil {
			t.Fatalf("畸形输入 %q 应当被拒绝", tc.raw)
		}
	}
}

func TestProposeCandidates(t *testing.T) {
	// 含逗号样本在“点小数+逗号分组”与三种逗号小数规则下均可解析，截断为 4 个。
	got, err := ProposeCandidates([]string{"1,234", "5,000", "42"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 4 {
		t.Fatalf("期望 4 个可行候选，得到 %d: %+v", len(got), got)
	}
	if got[0].Decimal != '.' || got[0].Grouping != GroupComma {
		t.Fatalf("首个候选应为逗号分组+点小数，得到 %+v", got[0])
	}
	wantDec := []byte{'.', ',', ',', '.'}
	wantGrp := []int{GroupComma, GroupDot, GroupNone, GroupSpace}
	for i := range got {
		if got[i].Decimal != wantDec[i] || got[i].Grouping != wantGrp[i] {
			t.Fatalf("第 %d 个候选顺序错误: %+v", i, got[i])
		}
	}

	// 同时含逗号与点小数的样本支持点小数+逗号分组，以及点小数+空格分组（空格不出现在样本里）。
	single, err := ProposeCandidates([]string{"1,234.5", "5,000.25"})
	if err != nil {
		t.Fatal(err)
	}
	if len(single) != 2 || single[0].Grouping != GroupComma || single[1].Grouping != GroupSpace {
		t.Fatalf("期望逗号分组与空格分组两个点小数候选，得到 %+v", single)
	}

	// 含字母样本没有任何规则可解析。
	none, err := ProposeCandidates([]string{"N/A", "1.0"})
	if err != nil {
		t.Fatal(err)
	}
	if len(none) != 0 {
		t.Fatalf("期望零候选，得到 %d", len(none))
	}
}

func TestScaleRat(t *testing.T) {
	r := big.NewRat(1234, 1000)
	scale := big.NewRat(1, 1000)
	ratEqual(t, ScaleRat(r, scale), "617/500000")
	if ScaleRat(nil, scale) != nil {
		t.Fatal("nil 输入应返回 nil")
	}
	ratEqual(t, ScaleRat(r, nil), "617/500")
}
