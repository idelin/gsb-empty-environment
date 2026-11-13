package grant

import (
	"testing"
	"time"
)

var testKey = bytes32(0x42)

func bytes32(b byte) []byte {
	k := make([]byte, 32)
	for i := range k {
		k[i] = b
	}
	return k
}

func allowAll(Principal, KeyResource, Context, Context) error { return nil }

func newTestStore(t *testing.T, clock Clock) *Store {
	t.Helper()
	if clock == nil {
		clock = NewFakeClock()
	}
	s, err := NewStore(testKey, clock, AuthorityFunc(allowAll))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	return s
}

func baseCreate() CreateRequest {
	return CreateRequest{
		GrantID:           "g1",
		RequestID:         "req1",
		Creator:           "alice",
		Key:               KeyResource{KeyID: "k1", Version: 1},
		GranteePrincipal:  "bob",
		RetiringPrincipal: "carol",
		Operations:        []Operation{OpDecrypt},
		Constraints:       Context{"env": "prod"},
		TTL:               time.Hour,
	}
}

func activeToken(t *testing.T, s *Store, req CreateRequest) string {
	t.Helper()
	_, tok, err := s.Create(req)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	return tok.Marshal()
}
