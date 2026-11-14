package netsec

import "testing"

func TestDecimalParseAndString(t *testing.T) {
	cases := map[string]string{
		"0":         "0",
		"7.10":      "7.1",
		"-0.05":     "-0.05",
		"1234.5678": "1234.5678",
		"7e3":       "7000",
		"1.5e-2":    "0.015",
		".5":        "0.5",
	}
	for in, want := range cases {
		d, err := NewDecimal(in)
		if err != nil {
			t.Fatalf("NewDecimal(%q): %v", in, err)
		}
		if got := d.String(); got != want {
			t.Errorf("NewDecimal(%q).String() = %s, want %s", in, got, want)
		}
	}
	if _, err := NewDecimal("1.2.3"); err == nil {
		t.Error("expected error for malformed input")
	}
}

func TestDecimalArithmeticExact(t *testing.T) {
	// 0.1 + 0.2 在二进制浮点下不精确,十进制下必须严格等于 0.3。
	sum := MustDecimal("0.1").Add(MustDecimal("0.2"))
	if !sum.Equal(MustDecimal("0.3")) {
		t.Fatalf("0.1 + 0.2 = %s", sum)
	}
	prod := MustDecimal("100.25").Mul(MustDecimal("7.10"))
	if got := prod.String(); got != "711.775" {
		t.Fatalf("100.25 * 7.10 = %s, want 711.775", got)
	}
	if !MustDecimal("1.0").Equal(MustDecimal("1.00")) {
		t.Fatal("1.0 should equal 1.00")
	}
}

func TestDecimalQuantize(t *testing.T) {
	cases := []struct {
		in    string
		scale int32
		mode  RoundingMode
		want  string
	}{
		{"711.775", 2, RoundHalfUp, "711.78"},
		{"711.775", 2, RoundHalfEven, "711.78"},
		{"711.765", 2, RoundHalfEven, "711.76"}, // 6 为偶,舍去
		{"711.775", 2, RoundDown, "711.77"},
		{"711.771", 2, RoundUp, "711.78"},
		{"-711.775", 2, RoundHalfUp, "-711.78"},
		{"-711.775", 2, RoundDown, "-711.77"},
		{"0.71178", 2, RoundHalfUp, "0.71"},
		{"5", 2, RoundHalfUp, "5"},
	}
	for _, c := range cases {
		got := MustDecimal(c.in).Quantize(c.scale, c.mode).String()
		if got != c.want {
			t.Errorf("Quantize(%s, %d, %s) = %s, want %s", c.in, c.scale, c.mode, got, c.want)
		}
	}
}

func TestDecimalJSONRoundTrip(t *testing.T) {
	d := MustDecimal("1234.5678")
	b, err := d.MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != `"1234.5678"` {
		t.Fatalf("marshal = %s", b)
	}
	var back Decimal
	if err := back.UnmarshalJSON(b); err != nil {
		t.Fatal(err)
	}
	if !back.Equal(d) {
		t.Fatalf("round trip: %s != %s", back, d)
	}
}
