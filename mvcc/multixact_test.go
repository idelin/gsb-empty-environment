package mvcc

import (
	"errors"
	"testing"
)

func TestMultiXactStatusPredicates(t *testing.T) {
	locks := []MultiXactStatus{MultiXactStatusForKeyShare, MultiXactStatusForShare}
	for _, s := range locks {
		if !s.IsLock() || s.IsUpdate() {
			t.Fatalf("status %d must be a pure lock", s)
		}
	}
	updates := []MultiXactStatus{
		MultiXactStatusForNoKeyUpdate,
		MultiXactStatusForUpdate,
		MultiXactStatusForDelete,
	}
	for _, s := range updates {
		if s.IsLock() || !s.IsUpdate() {
			t.Fatalf("status %d must be an updater", s)
		}
	}
}

func TestMultiXactStoreCreateAndRead(t *testing.T) {
	store := NewMultiXactStore()
	members := []MultiXactMember{
		{Xid: 10, Status: MultiXactStatusForKeyShare},
		{Xid: 11, Status: MultiXactStatusForShare},
		{Xid: 12, Status: MultiXactStatusForUpdate},
	}
	id, err := store.Create(members)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	if id != FirstMultiXactId {
		t.Fatalf("first id = %d, want %d", id, FirstMultiXactId)
	}

	got, err := store.Members(id)
	if err != nil {
		t.Fatalf("Members failed: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("member count = %d, want 3", len(got))
	}

	// 返回的是副本，外部修改不得污染存储。
	got[0].Xid = 999
	again, _ := store.Members(id)
	if again[0].Xid != 10 {
		t.Fatal("MultiXactStore must return defensive copies")
	}

	if _, err := store.Members(4242); !errors.Is(err, ErrInvalidMultiXact) {
		t.Fatalf("missing multi must return ErrInvalidMultiXact, got %v", err)
	}
}

func TestMultiXactStoreValidation(t *testing.T) {
	store := NewMultiXactStore()

	_, err := store.Create([]MultiXactMember{{Xid: FrozenXid, Status: MultiXactStatusForShare}})
	if !errors.Is(err, ErrInvalidMemberXid) {
		t.Fatalf("special member XID must be rejected, got %v", err)
	}

	_, err = store.Create([]MultiXactMember{
		{Xid: 10, Status: MultiXactStatusForUpdate},
		{Xid: 11, Status: MultiXactStatusForDelete},
	})
	if !errors.Is(err, ErrMultipleUpdaters) {
		t.Fatalf("two updaters must be rejected, got %v", err)
	}
}

func TestMultiXactStoreWraparound(t *testing.T) {
	store := NewMultiXactStore()
	store.mu.Lock()
	store.next = MaxMultiXactIdForTest
	store.mu.Unlock()

	id1, err := store.Create(nil)
	if err != nil {
		t.Fatal(err)
	}
	id2, err := store.Create(nil)
	if err != nil {
		t.Fatal(err)
	}
	if id1 != MultiXactId(^uint32(0)) || id2 != FirstMultiXactId {
		t.Fatalf("wraparound allocation = %d then %d", id1, id2)
	}
}

// MaxMultiXactIdForTest 是 32 位空间内最后一个正常 MultiXact 编号。
const MaxMultiXactIdForTest MultiXactId = ^MultiXactId(0)

func TestMultiXactIdPrecedes(t *testing.T) {
	if !MultiXactIdPrecedes(MaxMultiXactIdForTest, FirstMultiXactId) {
		t.Fatal("max multi must precede wraparound multi")
	}
	if MultiXactIdPrecedes(FirstMultiXactId, InvalidMultiXactId) {
		t.Fatal("InvalidMultiXactId must stay before every normal id")
	}
	if !MultiXactIdPrecedes(InvalidMultiXactId, FirstMultiXactId) {
		t.Fatal("InvalidMultiXactId must precede every normal id")
	}
}
