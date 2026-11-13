package netsec

import (
	"fmt"
	"strings"
)

// DType is the element type of a tensor.
type DType string

const (
	Float16        DType = "float16"
	Float32        DType = "float32"
	Float64        DType = "float64"
	BFloat16       DType = "bfloat16"
	Float8E4M3FN   DType = "float8e4m3fn"
	Float8E4M3FNUZ DType = "float8e4m3fnuz"
	Float8E5M2     DType = "float8e5m2"
	Float8E5M2FNUZ DType = "float8e5m2fnuz"
	Int8           DType = "int8"
	Uint8          DType = "uint8"
	Int16          DType = "int16"
	Uint16         DType = "uint16"
	Int32          DType = "int32"
	Uint32         DType = "uint32"
	Int64          DType = "int64"
	Uint64         DType = "uint64"
	Bool           DType = "bool"
	Complex64      DType = "complex64"
	Complex128     DType = "complex128"
)

var dtypeSizes = map[DType]int64{
	Float16:        2,
	Float32:        4,
	Float64:        8,
	BFloat16:       2,
	Float8E4M3FN:   1,
	Float8E4M3FNUZ: 1,
	Float8E5M2:     1,
	Float8E5M2FNUZ: 1,
	Int8:           1,
	Uint8:          1,
	Int16:          2,
	Uint16:         2,
	Int32:          4,
	Uint32:         4,
	Int64:          8,
	Uint64:         8,
	Bool:           1,
	Complex64:      8,
	Complex128:     16,
}

var dtypeAliases = map[string]DType{
	"float":    Float32,
	"f32":      Float32,
	"fp32":     Float32,
	"single":   Float32,
	"double":   Float64,
	"f64":      Float64,
	"fp64":     Float64,
	"half":     Float16,
	"f16":      Float16,
	"fp16":     Float16,
	"bf16":     BFloat16,
	"i8":       Int8,
	"u8":       Uint8,
	"i16":      Int16,
	"u16":      Uint16,
	"i32":      Int32,
	"u32":      Uint32,
	"i64":      Int64,
	"u64":      Uint64,
	"boolean":  Bool,
	"c64":      Complex64,
	"c128":     Complex128,
	"float8":   Float8E4M3FN,
	"fp8":      Float8E4M3FN,
	"float8e4": Float8E4M3FN,
	"float8e5": Float8E5M2,
}

// CanonicalDType normalizes dtype and reports whether it is supported.
func CanonicalDType(dtype DType) (DType, bool) {
	name := strings.ToLower(strings.TrimSpace(string(dtype)))
	if canonical, ok := dtypeAliases[name]; ok {
		return canonical, true
	}
	candidate := DType(name)
	if _, ok := dtypeSizes[candidate]; ok {
		return candidate, true
	}
	return "", false
}

// Size returns the number of bytes occupied by one element of dtype.
func (dtype DType) Size() (int64, bool) {
	canonical, ok := CanonicalDType(dtype)
	if !ok {
		return 0, false
	}
	return dtypeSizes[canonical], true
}

func normalizeDType(dtype DType) (DType, int64, error) {
	canonical, ok := CanonicalDType(dtype)
	if !ok {
		return "", 0, fmt.Errorf("%w: %q", ErrUnsupportedDType, string(dtype))
	}
	return canonical, dtypeSizes[canonical], nil
}
