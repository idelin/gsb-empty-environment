package netsec

import (
	"fmt"
	"io"
	"math"
	"runtime"
)

type tensorMetadata struct {
	descriptor TensorDescriptor
	hasShape   bool
	hasDType   bool
}

func metadataOf(handle TensorHandle) tensorMetadata {
	if described, ok := handle.(TensorDescriptorProvider); ok {
		return tensorMetadata{
			descriptor: described.Descriptor(),
			hasShape:   true,
			hasDType:   true,
		}
	}

	var meta tensorMetadata
	if provider, ok := handle.(TensorShapeProvider); ok {
		meta.descriptor.Shape = provider.Shape()
		meta.hasShape = true
	}
	if provider, ok := handle.(TensorDTypeProvider); ok {
		meta.descriptor.DType = provider.DType()
		meta.hasDType = true
	}
	return meta
}

func validateDescriptor(meta tensorMetadata, expectedShape TensorShape, expectedDType DType) error {
	if meta.hasShape && meta.descriptor.Shape != expectedShape {
		return fmt.Errorf("%w: handle shape %+v, want %+v", ErrShapeMismatch, meta.descriptor.Shape, expectedShape)
	}
	if meta.hasDType && meta.descriptor.DType != expectedDType {
		return fmt.Errorf("%w: handle dtype %q, want %q", ErrDTypeMismatch, meta.descriptor.DType, expectedDType)
	}
	return nil
}

func expectedTensorBytes(shape TensorShape, dtype DType, maxBytes int64) (int64, error) {
	if shape.Rows <= 0 || shape.Cols <= 0 {
		return 0, fmt.Errorf("%w: non-positive shape %+v", ErrShapeMismatch, shape)
	}
	elementSize, ok := dtype.elementSize()
	if !ok {
		return 0, fmt.Errorf("%w: unsupported dtype %q", ErrDTypeNotAllowed, dtype)
	}
	if int64(shape.Rows) > math.MaxInt64/int64(shape.Cols) {
		return 0, fmt.Errorf("%w: shape overflows", ErrShapeMismatch)
	}
	elements := int64(shape.Rows) * int64(shape.Cols)
	if elements > math.MaxInt64/elementSize {
		return 0, fmt.Errorf("%w: byte length overflows", ErrShapeMismatch)
	}
	bytes := elements * elementSize
	if maxBytes > 0 && bytes > maxBytes {
		return 0, fmt.Errorf("%w: %d > %d", ErrTensorTooLarge, bytes, maxBytes)
	}
	return bytes, nil
}

func readAndVerifyTensor(
	provider TensorProvider,
	digest Digest,
	expectedShape TensorShape,
	dtype DType,
	maxBytes int64,
) ([]byte, error) {
	expectedBytes, err := expectedTensorBytes(expectedShape, dtype, maxBytes)
	if err != nil {
		return nil, err
	}
	handle, err := provider.OpenTensor(digest)
	if err != nil {
		return nil, fmt.Errorf("%w: open %q: %v", ErrTensorRead, digest, err)
	}
	if handle == nil {
		return nil, fmt.Errorf("%w: nil handle for %q", ErrTensorRead, digest)
	}
	meta := metadataOf(handle)
	if err := validateDescriptor(meta, expectedShape, dtype); err != nil {
		return nil, err
	}

	stream, err := handle.OpenReadOnly()
	if err != nil {
		return nil, fmt.Errorf("%w: open stream %q: %v", ErrTensorRead, digest, err)
	}
	if stream == nil {
		return nil, fmt.Errorf("%w: nil stream for %q", ErrTensorRead, digest)
	}

	readLimit := expectedBytes
	if readLimit < math.MaxInt64 {
		readLimit++
	}
	if maxBytes > 0 && maxBytes < math.MaxInt64 && readLimit > maxBytes+1 {
		readLimit = maxBytes + 1
	}
	data, readErr := io.ReadAll(io.LimitReader(stream, readLimit))
	closeErr := stream.Close()
	if readErr != nil {
		zeroBytes(data)
		return nil, fmt.Errorf("%w: read %q: %v", ErrTensorRead, digest, readErr)
	}
	if closeErr != nil {
		zeroBytes(data)
		return nil, fmt.Errorf("%w: close %q: %v", ErrTensorRead, digest, closeErr)
	}
	if int64(len(data)) != expectedBytes {
		zeroBytes(data)
		return nil, fmt.Errorf("%w: got %d bytes, want %d", ErrTensorLength, len(data), expectedBytes)
	}
	if actual := DigestOf(data); actual != digest {
		zeroBytes(data)
		return nil, fmt.Errorf("%w: got %s, want %s", ErrDigestMismatch, actual, digest)
	}
	if err := validateFinite(data, dtype); err != nil {
		zeroBytes(data)
		return nil, err
	}
	return data, nil
}

func validateFinite(data []byte, dtype DType) error {
	switch dtype {
	case DTypeFloat16:
		for i := 0; i+1 < len(data); i += 2 {
			bits := uint16(data[i]) | uint16(data[i+1])<<8
			if bits&0x7c00 == 0x7c00 {
				return fmt.Errorf("%w: %s element %d", ErrNonFinite, dtype, i/2)
			}
		}
	case DTypeBFloat16:
		for i := 0; i+1 < len(data); i += 2 {
			bits := uint16(data[i]) | uint16(data[i+1])<<8
			if bits&0x7f80 == 0x7f80 {
				return fmt.Errorf("%w: %s element %d", ErrNonFinite, dtype, i/2)
			}
		}
	case DTypeFloat32:
		for i := 0; i+3 < len(data); i += 4 {
			bits := uint32(data[i]) |
				uint32(data[i+1])<<8 |
				uint32(data[i+2])<<16 |
				uint32(data[i+3])<<24
			if bits&0x7f800000 == 0x7f800000 {
				return fmt.Errorf("%w: %s element %d", ErrNonFinite, dtype, i/4)
			}
		}
	case DTypeFloat64:
		for i := 0; i+7 < len(data); i += 8 {
			bits := uint64(data[i]) |
				uint64(data[i+1])<<8 |
				uint64(data[i+2])<<16 |
				uint64(data[i+3])<<24 |
				uint64(data[i+4])<<32 |
				uint64(data[i+5])<<40 |
				uint64(data[i+6])<<48 |
				uint64(data[i+7])<<56
			if bits&0x7ff0000000000000 == 0x7ff0000000000000 {
				return fmt.Errorf("%w: %s element %d", ErrNonFinite, dtype, i/8)
			}
		}
	default:
		return fmt.Errorf("%w: unsupported dtype %q", ErrDTypeNotAllowed, dtype)
	}
	return nil
}

func zeroBytes(data []byte) {
	for i := range data {
		data[i] = 0
	}
	runtime.KeepAlive(data)
}
