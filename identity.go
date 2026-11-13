package netsec

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"time"
)

// IdentityDigest returns a stable SHA-256 identity for a request. It includes
// the tenant as well as the tenant-local request ID, so two tenants that use
// the same request ID can never share a digest.
func IdentityDigest(req Request) string {
	h := sha256.New()
	writeString(h, req.TenantID)
	writeString(h, req.RequestID)
	writeUint64(h, req.ModelGeneration)
	cancelGeneration := req.CancelGeneration
	if cancelGeneration == 0 {
		cancelGeneration = req.CancellationGeneration
	}
	writeUint64(h, cancelGeneration)
	writeUint64(h, uint64(req.OutputLimit()))
	strategy := req.DecodeStrategy
	if strategy == "" {
		strategy = req.DecodingStrategy
	}
	writeString(h, strategy)
	if req.Deadline.IsZero() {
		writeUint64(h, 0)
	} else {
		writeUint64(h, 1)
		writeUint64(h, uint64(req.Deadline.UTC().UnixNano()))
	}
	writeUint64(h, uint64(len(req.InputTokens)))
	for _, token := range req.InputTokens {
		writeUint32(h, token)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// ComputeIdentityDigest is an alias for IdentityDigest.
func ComputeIdentityDigest(req Request) string { return IdentityDigest(req) }

func writeString(h interface{ Write([]byte) (int, error) }, value string) {
	var size [8]byte
	binary.BigEndian.PutUint64(size[:], uint64(len(value)))
	_, _ = h.Write(size[:])
	_, _ = h.Write([]byte(value))
}

func writeUint64(h interface{ Write([]byte) (int, error) }, value uint64) {
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], value)
	_, _ = h.Write(buf[:])
}

func writeUint32(h interface{ Write([]byte) (int, error) }, value uint32) {
	var buf [4]byte
	binary.BigEndian.PutUint32(buf[:], value)
	_, _ = h.Write(buf[:])
}

func normalizeRequest(req Request) (Request, error) {
	if req.TenantID == "" {
		return Request{}, fmt.Errorf("%w: tenant ID is empty", ErrInvalidRequest)
	}
	if req.RequestID == "" {
		return Request{}, fmt.Errorf("%w: request ID is empty", ErrInvalidRequest)
	}
	if req.MaxOutputTokens < 0 {
		return Request{}, fmt.Errorf("%w: max output tokens is negative", ErrInvalidRequest)
	}
	if req.MaxOutputLength < 0 {
		return Request{}, fmt.Errorf("%w: max output length is negative", ErrInvalidRequest)
	}
	if req.MaxNewTokens < 0 {
		return Request{}, fmt.Errorf("%w: max new tokens is negative", ErrInvalidRequest)
	}
	limit := 0
	for _, candidate := range []int{req.MaxOutputTokens, req.MaxOutputLength, req.MaxNewTokens} {
		if candidate == 0 {
			continue
		}
		if limit != 0 && limit != candidate {
			return Request{}, fmt.Errorf("%w: conflicting output limits", ErrInvalidRequest)
		}
		limit = candidate
	}
	req.MaxOutputTokens = limit
	req.MaxOutputLength = limit
	req.MaxNewTokens = limit
	if req.CancelGeneration != 0 && req.CancellationGeneration != 0 &&
		req.CancelGeneration != req.CancellationGeneration {
		return Request{}, fmt.Errorf("%w: conflicting cancellation generations", ErrInvalidRequest)
	}
	if req.CancelGeneration == 0 {
		req.CancelGeneration = req.CancellationGeneration
	}
	req.CancellationGeneration = req.CancelGeneration
	if req.ArrivalSequence == 0 {
		req.ArrivalSequence = req.ArrivalOrder
	}
	req.ArrivalOrder = req.ArrivalSequence
	if req.DecodeStrategy != "" && req.DecodingStrategy != "" &&
		req.DecodeStrategy != req.DecodingStrategy {
		return Request{}, fmt.Errorf("%w: conflicting decoding strategies", ErrInvalidRequest)
	}
	if req.DecodeStrategy == "" {
		req.DecodeStrategy = req.DecodingStrategy
	}
	req.DecodingStrategy = req.DecodeStrategy
	req.InputTokens = cloneTokens(req.InputTokens)
	if !req.Deadline.IsZero() {
		req.Deadline = req.Deadline.UTC()
	}
	return req, nil
}

func digestEqual(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	var diff byte
	for i := 0; i < len(a); i++ {
		diff |= a[i] ^ b[i]
	}
	return diff == 0
}

func deadlinePassed(deadline, now time.Time) bool {
	return !deadline.IsZero() && now.After(deadline)
}
