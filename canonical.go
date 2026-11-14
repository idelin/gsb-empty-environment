package netsec

import (
	"crypto/hmac"
	"crypto/sha256"
	"strconv"
)

// canonicalFields is the fixed, ordered set of fields covered by the
// signature. Every value is length-prefixed so that values containing
// delimiters cannot be confused for field boundaries, which would otherwise
// let an attacker shift data between queue, topic and object.
var canonicalFields = []string{"function", "topic", "queue", "idempotency", "object"}

// canonicalEvent builds the deterministic byte representation that is signed
// by the trusted event bus and verified by the function. The trigger id is
// included implicitly through the key used to verify; all attacker-controlled
// routing and payload fields are included explicitly.
func canonicalEvent(e InboundEvent) []byte {
	values := map[string]string{
		"function":    e.FunctionID,
		"topic":       e.Topic,
		"queue":       e.Queue,
		"idempotency": e.IdempotencyKey,
		"object":      string(e.Object),
	}

	var buf []byte
	for _, name := range canonicalFields {
		buf = appendField(buf, name)
		buf = appendField(buf, values[name])
	}
	return buf
}

// appendField writes a length-prefixed field. The length is the number of
// UTF-8 bytes in the value, terminated by ':'.
func appendField(dst []byte, value string) []byte {
	dst = strconv.AppendInt(dst, int64(len(value)), 10)
	dst = append(dst, ':')
	dst = append(dst, value...)
	return dst
}

// sign computes the HMAC-SHA256 of the canonical event under key.
func sign(key []byte, e InboundEvent) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write(canonicalEvent(e))
	return mac.Sum(nil)
}

// validSignature reports whether the provided signature matches the canonical
// event. Comparison is constant time.
func validSignature(key, signature []byte, e InboundEvent) bool {
	if len(signature) == 0 {
		return false
	}
	return hmac.Equal(signature, sign(key, e))
}

// SignEvent signs an event with the trigger's signing key and stores the raw
// signature on the envelope. It mirrors the operation performed by the
// trusted event bus and is intended for publishers and tests.
func SignEvent(e *InboundEvent, key []byte) {
	e.Signature = sign(key, *e)
}
