package netsec

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
)

func testFields() []FieldSpec {
	return []FieldSpec{
		{Number: 2, Kind: LLVAR, MaxLength: 10},
		{Number: 3, Kind: Fixed, MaxLength: 6},
		{Number: 52, Kind: Fixed, MaxLength: pinBlockLength},
		{Number: 102, Kind: LLLVAR, MaxLength: 999},
		{Number: 120, Kind: LLVAR, MaxLength: 5},
		{Number: 128, Kind: Fixed, MaxLength: 8},
	}
}

// buildMessage 按字段号升序拼装从主位图开始的报文体。
// 变长字段需自带长度前缀，定长字段直接给数据。
func buildMessage(t *testing.T, fields map[int][]byte) []byte {
	t.Helper()
	numbers := make([]int, 0, len(fields))
	for n := range fields {
		numbers = append(numbers, n)
	}
	// 插入排序，字段数很少。
	for i := 1; i < len(numbers); i++ {
		for j := i; j > 0 && numbers[j-1] > numbers[j]; j-- {
			numbers[j-1], numbers[j] = numbers[j], numbers[j-1]
		}
	}

	var bitmap [16]byte
	bitmapLen := primaryBytes
	for _, n := range numbers {
		if n > primaryBits {
			bitmapLen = primaryBytes + secondaryBytes
			// 位 1 是次级位图存在标志，必须在主位图首字节置位。
			bitmap[0] |= 0x80
		}
		bitIndex := n - 1
		bitmap[bitIndex/8] |= 1 << (7 - bitIndex%8)
	}

	var body bytes.Buffer
	body.Write(bitmap[:bitmapLen])
	for _, n := range numbers {
		body.Write(fields[n])
	}
	return body.Bytes()
}

func TestAdmitValidMessage(t *testing.T) {
	a, err := NewAdmission(testFields(), []string{"0200"})
	if err != nil {
		t.Fatalf("NewAdmission: %v", err)
	}

	body := buildMessage(t, map[int][]byte{
		2:  append([]byte{3}, []byte("abc")...),
		3:  []byte("123456"),
		52: []byte("12345678"),
	})
	if err := a.Admit("0200", body); err != nil {
		t.Fatalf("Admit valid message: %v", err)
	}
}

func TestRejectsUndefinedPrimaryBit(t *testing.T) {
	a, err := NewAdmission(testFields(), []string{"0200"})
	if err != nil {
		t.Fatalf("NewAdmission: %v", err)
	}

	// 字段 4 未在字段表登记。
	body := buildMessage(t, map[int][]byte{
		3: []byte("123456"),
		4: []byte("0000"),
	})
	err = a.Admit("0200", body)
	if !errors.Is(err, ErrUndefinedBit) {
		t.Fatalf("want ErrUndefinedBit, got %v", err)
	}
	if err != nil && !strings.Contains(err.Error(), "field 4") {
		t.Fatalf("error should name offending field: %v", err)
	}
}

func TestRejectsUndefinedSecondaryBit(t *testing.T) {
	a, err := NewAdmission(testFields(), []string{"0200"})
	if err != nil {
		t.Fatalf("NewAdmission: %v", err)
	}

	// 字段 66 未登记，位 1 必须随次级位图自动置位。
	body := buildMessage(t, map[int][]byte{
		3:  []byte("123456"),
		66: []byte("x"),
	})
	err = a.Admit("0200", body)
	if !errors.Is(err, ErrUndefinedBit) {
		t.Fatalf("want ErrUndefinedBit for field 66, got %v", err)
	}
}

func TestSecondaryBitmapOnlyWhenBitOneSet(t *testing.T) {
	a, err := NewAdmission(testFields(), []string{"0200"})
	if err != nil {
		t.Fatalf("NewAdmission: %v", err)
	}

	// 位 1 未置位：只有 8 字节主位图，随后的字节全部属于字段数据，
	// 字段 3 的 6 字节定长数据正好消费掉，报文合法。
	body := append(make([]byte, primaryBytes), []byte("123456")...)
	body[0] = 0x20 // 仅置位 3，不置位 1。
	if err := a.Admit("0200", body); err != nil {
		t.Fatalf("message without secondary bitmap must not read one: %v", err)
	}

	// 位 1 置位却缺少次级位图的 8 字节，必须报截断。
	truncated := make([]byte, primaryBytes)
	truncated[0] = 0x80
	err = a.Admit("0200", truncated)
	if !errors.Is(err, ErrTruncated) {
		t.Fatalf("want ErrTruncated for missing secondary bitmap, got %v", err)
	}
}

func TestRejectsLLVARExceedingRemainingBytes(t *testing.T) {
	a, err := NewAdmission(testFields(), []string{"0200"})
	if err != nil {
		t.Fatalf("NewAdmission: %v", err)
	}

	// 声明 10 字节但只给 3 字节数据。
	body := buildMessage(t, map[int][]byte{
		2: append([]byte{10}, []byte("abc")...),
	})
	err = a.Admit("0200", body)
	if !errors.Is(err, ErrLengthOverflow) {
		t.Fatalf("want ErrLengthOverflow, got %v", err)
	}
}

func TestRejectsLLVARExceedingFieldLimit(t *testing.T) {
	a, err := NewAdmission(testFields(), []string{"0200"})
	if err != nil {
		t.Fatalf("NewAdmission: %v", err)
	}

	data := make([]byte, 20)
	body := buildMessage(t, map[int][]byte{
		2: append([]byte{20}, data...), // 字段 2 上限 10 字节。
	})
	err = a.Admit("0200", body)
	if !errors.Is(err, ErrLengthOverflow) {
		t.Fatalf("want ErrLengthOverflow for field limit, got %v", err)
	}
}

func TestRejectsLLLVAROverflow(t *testing.T) {
	a, err := NewAdmission(testFields(), []string{"0200"})
	if err != nil {
		t.Fatalf("NewAdmission: %v", err)
	}

	// LLLVAR 两字节大端前缀声明 0x0100=256，实际无数据。
	body := buildMessage(t, map[int][]byte{
		102: {0x01, 0x00},
	})
	err = a.Admit("0200", body)
	if !errors.Is(err, ErrLengthOverflow) {
		t.Fatalf("want ErrLengthOverflow for LLLVAR, got %v", err)
	}
}

func TestPINBlockForbiddenMTI(t *testing.T) {
	a, err := NewAdmission(testFields(), []string{"0200"})
	if err != nil {
		t.Fatalf("NewAdmission: %v", err)
	}

	body := buildMessage(t, map[int][]byte{
		52: []byte("12345678"),
	})
	err = a.Admit("0210", body) // 0210 未登记允许 PIN 块。
	if !errors.Is(err, ErrPINForbidden) {
		t.Fatalf("want ErrPINForbidden, got %v", err)
	}
}

func TestPINBlockAllowedMTI(t *testing.T) {
	a, err := NewAdmission(testFields(), []string{"0200", "0210"})
	if err != nil {
		t.Fatalf("NewAdmission: %v", err)
	}

	body := buildMessage(t, map[int][]byte{
		52: []byte("12345678"),
	})
	if err := a.Admit("0210", body); err != nil {
		t.Fatalf("registered MTI should permit 52 field: %v", err)
	}
}

func TestPINFieldMustBeEightBytes(t *testing.T) {
	bad := []FieldSpec{
		{Number: 52, Kind: LLVAR, MaxLength: 8},
	}
	if _, err := NewAdmission(bad, []string{"0200"}); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("LLVAR 52 field must be rejected: %v", err)
	}

	bad = []FieldSpec{
		{Number: 52, Kind: Fixed, MaxLength: 6},
	}
	if _, err := NewAdmission(bad, []string{"0200"}); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("6-byte fixed 52 field must be rejected: %v", err)
	}
}

func TestTruncatedFixedField(t *testing.T) {
	a, err := NewAdmission(testFields(), []string{"0200"})
	if err != nil {
		t.Fatalf("NewAdmission: %v", err)
	}

	body := buildMessage(t, map[int][]byte{
		3: []byte("123"), // 定长 6 字节却只给 3 字节。
	})
	err = a.Admit("0200", body)
	if !errors.Is(err, ErrTruncated) {
		t.Fatalf("want ErrTruncated, got %v", err)
	}
}

func TestInvalidMTI(t *testing.T) {
	a, err := NewAdmission(testFields(), []string{"0200"})
	if err != nil {
		t.Fatalf("NewAdmission: %v", err)
	}

	for _, mti := range []string{"", "200", "020A", "02000"} {
		if err := a.Admit(mti, make([]byte, primaryBytes)); !errors.Is(err, ErrInvalidMTI) {
			t.Fatalf("mti %q: want ErrInvalidMTI, got %v", mti, err)
		}
	}
}

func TestInvalidConfig(t *testing.T) {
	cases := map[string][]FieldSpec{
		"field one reserved":      {{Number: 1, Kind: Fixed, MaxLength: 8}},
		"field above 128":         {{Number: 129, Kind: Fixed, MaxLength: 8}},
		"non-positive max length": {{Number: 10, Kind: Fixed, MaxLength: 0}},
		"duplicate field": {
			{Number: 10, Kind: Fixed, MaxLength: 4},
			{Number: 10, Kind: LLVAR, MaxLength: 4},
		},
	}
	for name, fields := range cases {
		if _, err := NewAdmission(fields, nil); !errors.Is(err, ErrInvalidConfig) {
			t.Fatalf("%s: want ErrInvalidConfig, got %v", name, err)
		}
	}

	if _, err := NewAdmission(testFields(), []string{"abcd"}); !errors.Is(err, ErrInvalidMTI) {
		t.Fatalf("invalid registered MTI: want ErrInvalidMTI, got %v", err)
	}
}

func TestLoggerDiscardsAllOutput(t *testing.T) {
	a, err := NewAdmission(testFields(), []string{"0200"})
	if err != nil {
		t.Fatalf("NewAdmission: %v", err)
	}

	// 触发所有可能记录日志的拒绝路径，保证即使将来误加日志调用，
	// 包内唯一日志出口仍然丢弃全部输出。
	undefined := buildMessage(t, map[int][]byte{4: []byte("0000")})
	_ = a.Admit("0200", undefined)

	overflow := buildMessage(t, map[int][]byte{
		2: append([]byte{10}, []byte("abc")...),
	})
	_ = a.Admit("0200", overflow)

	pin := buildMessage(t, map[int][]byte{52: []byte("12345678")})
	_ = a.Admit("0210", pin)

	// 默认 logger 的输出端必须是 io.Discard，保证任何包内日志都不落盘。
	if logger.Writer() != io.Discard {
		t.Fatalf("default logger writer = %v, want io.Discard", logger.Writer())
	}
}

func TestValidVariableFieldsWithSecondaryBitmap(t *testing.T) {
	a, err := NewAdmission(testFields(), []string{"0200"})
	if err != nil {
		t.Fatalf("NewAdmission: %v", err)
	}

	body := buildMessage(t, map[int][]byte{
		3:   []byte("123456"),
		102: append([]byte{0x00, 0x02}, []byte("xy")...),
		120: append([]byte{2}, []byte("ab")...),
		128: []byte("87654321"),
	})
	if err := a.Admit("0200", body); err != nil {
		t.Fatalf("valid secondary-bitmap message: %v", err)
	}
}
