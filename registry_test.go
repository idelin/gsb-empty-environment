package netsec

import (
	"errors"
	"testing"
)

func TestRegisterAcquireRelease(t *testing.T) {
	reg := NewRegistry()
	tok := reg.Register("tenant-a", []string{"read", "write"})

	if err := reg.acquireAll("tenant-a", []string{tok}); err != nil {
		t.Fatalf("acquire: %v", err)
	}
	if got := reg.ActiveLeases(tok); got != 1 {
		t.Fatalf("ActiveLeases = %d, want 1", got)
	}
	reg.releaseAll([]string{tok})
	if got := reg.ActiveLeases(tok); got != 0 {
		t.Fatalf("ActiveLeases after release = %d, want 0", got)
	}
	// 重复释放不应出现负计数。
	reg.releaseAll([]string{tok})
	if got := reg.ActiveLeases(tok); got != 0 {
		t.Fatalf("ActiveLeases after double release = %d, want 0", got)
	}
}

func TestRevokeBlocksNewLeasesButExistingComplete(t *testing.T) {
	reg := NewRegistry()
	tok := reg.Register("tenant-a", []string{"read"})

	if err := reg.acquireAll("tenant-a", []string{tok}); err != nil {
		t.Fatalf("acquire: %v", err)
	}
	reg.Revoke(tok)

	// 撤销返回后不能取得新租约。
	if err := reg.acquireAll("tenant-a", []string{tok}); !errors.Is(err, ErrRevoked) {
		t.Fatalf("acquire after revoke = %v, want ErrRevoked", err)
	}
	// 已有租约可完成：撤销不清除在途租约。
	if got := reg.ActiveLeases(tok); got != 1 {
		t.Fatalf("ActiveLeases after revoke = %d, want 1", got)
	}
	reg.releaseAll([]string{tok})
	if got := reg.ActiveLeases(tok); got != 0 {
		t.Fatalf("ActiveLeases after release = %d, want 0", got)
	}
	// 撤销幂等。
	reg.Revoke(tok)
}

func TestStaleGenerationRejected(t *testing.T) {
	reg := NewRegistry()
	old := reg.Register("tenant-a", []string{"read"})
	cur, ok := reg.Reissue(old)
	if !ok {
		t.Fatal("Reissue failed")
	}
	if old == cur {
		t.Fatal("Reissue returned identical token")
	}
	if err := reg.acquireAll("tenant-a", []string{old}); !errors.Is(err, ErrStaleGeneration) {
		t.Fatalf("acquire old generation = %v, want ErrStaleGeneration", err)
	}
	if err := reg.acquireAll("tenant-a", []string{cur}); err != nil {
		t.Fatalf("acquire current generation: %v", err)
	}
}

func TestReissueRevokedOrUnknownFails(t *testing.T) {
	reg := NewRegistry()
	tok := reg.Register("tenant-a", nil)
	reg.Revoke(tok)
	if _, ok := reg.Reissue(tok); ok {
		t.Fatal("Reissue of revoked token succeeded")
	}
	if _, ok := reg.Reissue("no-such-token"); ok {
		t.Fatal("Reissue of unknown token succeeded")
	}
}

func TestCrossTenantRejected(t *testing.T) {
	reg := NewRegistry()
	tok := reg.Register("tenant-a", []string{"read"})
	if err := reg.acquireAll("tenant-b", []string{tok}); !errors.Is(err, ErrCrossTenant) {
		t.Fatalf("acquire as other tenant = %v, want ErrCrossTenant", err)
	}
	if got := reg.ActiveLeases(tok); got != 0 {
		t.Fatalf("ActiveLeases = %d, want 0", got)
	}
}

func TestUnknownTokenRejected(t *testing.T) {
	reg := NewRegistry()
	if err := reg.acquireAll("tenant-a", []string{"deadbeef"}); !errors.Is(err, ErrUnknownCapability) {
		t.Fatalf("acquire unknown token = %v, want ErrUnknownCapability", err)
	}
}

func TestAcquireAllIsAtomic(t *testing.T) {
	reg := NewRegistry()
	good := reg.Register("tenant-a", []string{"read"})
	bad := reg.Register("tenant-a", []string{"write"})
	reg.Revoke(bad)

	if err := reg.acquireAll("tenant-a", []string{good, bad}); !errors.Is(err, ErrRevoked) {
		t.Fatalf("acquireAll = %v, want ErrRevoked", err)
	}
	// 任一失败则一个租约也不产生。
	if got := reg.ActiveLeases(good); got != 0 {
		t.Fatalf("ActiveLeases(good) = %d, want 0", got)
	}
}
