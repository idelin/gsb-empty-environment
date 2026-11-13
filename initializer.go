package netsec

import (
	"fmt"
	"math/big"
	"unicode/utf8"
)

// Initializer describes one external tensor range. TensorID identifies the
// logical tensor. Location is a relative, slash-separated path below the model
// directory. Offset and Length are byte positions in that file, and SHA256 is
// the digest of exactly that range.
type Initializer struct {
	TensorID string
	Location string
	Offset   int64
	Length   int64
	SHA256   Digest
	Dtype    DType
	Shape    []int64
	Shared   bool
}

type normalizedInitializer struct {
	Initializer
	end int64
}

func normalizeInitializer(in Initializer) (normalizedInitializer, error) {
	if in.TensorID == "" {
		return normalizedInitializer{}, fmt.Errorf("%w: empty tensor id", ErrInvalidInitializer)
	}
	if !utf8.ValidString(in.TensorID) {
		return normalizedInitializer{}, fmt.Errorf("%w: tensor id is not valid UTF-8", ErrInvalidInitializer)
	}
	if len(in.TensorID) > 4096 {
		return normalizedInitializer{}, fmt.Errorf("%w: tensor id is too long", ErrInvalidInitializer)
	}
	if err := ValidateLocation(in.Location); err != nil {
		return normalizedInitializer{}, err
	}
	if in.Offset < 0 {
		return normalizedInitializer{}, fmt.Errorf("%w: negative offset %d", ErrInvalidInitializer, in.Offset)
	}
	if in.Length < 0 {
		return normalizedInitializer{}, fmt.Errorf("%w: negative length %d", ErrInvalidInitializer, in.Length)
	}
	end, err := checkedEnd(in.Offset, in.Length)
	if err != nil {
		return normalizedInitializer{}, err
	}
	if in.SHA256.IsZero() {
		return normalizedInitializer{}, fmt.Errorf("%w: missing SHA-256 digest", ErrInvalidInitializer)
	}

	dtype, elementSize, err := normalizeDType(in.Dtype)
	if err != nil {
		return normalizedInitializer{}, err
	}
	expected, err := tensorByteLength(elementSize, in.Shape)
	if err != nil {
		return normalizedInitializer{}, err
	}
	if in.Length != expected {
		return normalizedInitializer{}, fmt.Errorf(
			"%w: got %d bytes, want %d for %s%v",
			ErrInvalidTensorLength, in.Length, expected, dtype, in.Shape,
		)
	}

	in.Dtype = dtype
	in.Shape = append([]int64(nil), in.Shape...)
	return normalizedInitializer{Initializer: in, end: end}, nil
}

// TensorByteLength returns the exact byte length of a tensor with the given
// element type and dimensions. The calculation is performed with arbitrary
// precision integers and fails if the result does not fit in an int64.
func TensorByteLength(dtype DType, shape []int64) (int64, error) {
	_, elementSize, err := normalizeDType(dtype)
	if err != nil {
		return 0, err
	}
	return tensorByteLength(elementSize, shape)
}

func tensorByteLength(elementSize int64, shape []int64) (int64, error) {
	elements := big.NewInt(1)
	for i, dimension := range shape {
		if dimension < 0 {
			return 0, fmt.Errorf("%w: negative shape dimension %d at index %d", ErrInvalidInitializer, dimension, i)
		}
		elements.Mul(elements, big.NewInt(dimension))
		if !elements.IsInt64() {
			return 0, fmt.Errorf("%w: tensor element count overflows int64", ErrInvalidInitializer)
		}
	}
	elements.Mul(elements, big.NewInt(elementSize))
	if !elements.IsInt64() {
		return 0, fmt.Errorf("%w: tensor byte length overflows int64", ErrInvalidInitializer)
	}
	return elements.Int64(), nil
}

func checkedEnd(offset, length int64) (int64, error) {
	if offset < 0 || length < 0 {
		return 0, fmt.Errorf("%w: negative range", ErrInvalidInitializer)
	}
	end := new(big.Int).Add(big.NewInt(offset), big.NewInt(length))
	if !end.IsInt64() {
		return 0, fmt.Errorf("%w: offset plus length overflows int64", ErrInvalidInitializer)
	}
	return end.Int64(), nil
}

func checkedRangeInFile(offset, length, size int64) error {
	if size < 0 {
		return fmt.Errorf("%w: negative file size", ErrInvalidInitializer)
	}
	end, err := checkedEnd(offset, length)
	if err != nil {
		return err
	}
	if end > size {
		return fmt.Errorf(
			"%w: range [%d,%d) exceeds file size %d",
			ErrInvalidInitializer, offset, end, size,
		)
	}
	return nil
}

func initializersEqual(a, b Initializer) bool {
	if a.TensorID != b.TensorID ||
		a.Location != b.Location ||
		a.Offset != b.Offset ||
		a.Length != b.Length ||
		a.SHA256 != b.SHA256 ||
		a.Dtype != b.Dtype ||
		a.Shared != b.Shared ||
		len(a.Shape) != len(b.Shape) {
		return false
	}
	for i := range a.Shape {
		if a.Shape[i] != b.Shape[i] {
			return false
		}
	}
	return true
}

func shapesEqual(a, b []int64) bool {
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

func rangesOverlap(a, b normalizedInitializer) bool {
	if a.Offset == a.end || b.Offset == b.end {
		return false
	}
	return a.Offset < b.end && b.Offset < a.end
}

func overlapAllowed(a, b normalizedInitializer) bool {
	return a.TensorID == b.TensorID &&
		a.SHA256 == b.SHA256 &&
		a.Dtype == b.Dtype &&
		shapesEqual(a.Shape, b.Shape) &&
		a.Shared && b.Shared
}
