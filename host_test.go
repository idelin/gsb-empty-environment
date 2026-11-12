package netsec

import (
	"errors"
	"sync"
	"testing"
)

func TestMergeTagsPrecedence(t *testing.T) {
	merged, err := MergeTags(
		TagSet{"env": "prod", "team": "sec"},
		TagSet{"env": "dev", "region": "cn"},
		TagSet{"integrity": "high"},
	)
	if err != nil {
		t.Fatalf("MergeTags: %v", err)
	}
	want := TagSet{"env": "prod", "team": "sec", "region": "cn", "integrity": "high"}
	if Digest(merged) != Digest(want) {
		t.Fatalf("merged = %v, want %v", merged, want)
	}
}

func TestMergeTagsLockedConflict(t *testing.T) {
	_, err := MergeTags(
		TagSet{"integrity": "low"},
		nil,
		TagSet{"integrity": "high"},
	)
	if !errors.Is(err, ErrLockedTagConflict) {
		t.Fatalf("err = %v, want ErrLockedTagConflict", err)
	}

	// 请求值与锁定值相同则允许。
	if _, err := MergeTags(
		TagSet{"integrity": "high"},
		nil,
		TagSet{"integrity": "high"},
	); err != nil {
		t.Fatalf("same-value locked tag should pass: %v", err)
	}
}

func TestDigestCanonical(t *testing.T) {
	a := TagSet{"k1": "v1", "k2": "v2"}
	b := TagSet{"k2": "v2", "k1": "v1"}
	if Digest(a) != Digest(b) {
		t.Fatal("digest must be order-independent")
	}
	if Digest(a) == Digest(TagSet{"k1": "v1"}) {
		t.Fatal("digest must change with tag set")
	}
}

func newHostWithGrants(caller string, grantedDigests ...string) (*Host, *FailableStore) {
	grants := NewDigestGrantSet()
	for _, d := range grantedDigests {
		grants.Grant(caller, d)
	}
	fs := NewFailableStore(NewMemStore())
	h := NewHost(fs, grants,
		TagSet{"region": "cn"},
		TagSet{"integrity": "high"},
	)
	return h, fs
}

func finalDigest(t *testing.T, requested TagSet) string {
	t.Helper()
	merged, err := MergeTags(requested, TagSet{"region": "cn"}, TagSet{"integrity": "high"})
	if err != nil {
		t.Fatalf("MergeTags: %v", err)
	}
	return Digest(merged)
}

func TestCreateSuccessBindsFinalDigest(t *testing.T) {
	caller := "alice"
	requested := TagSet{"env": "prod"}
	h, _ := newHostWithGrants(caller, finalDigest(t, requested))

	id, err := h.Create(CreateRequest{Caller: caller, Tags: requested})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	obj, err := h.Read(caller, id)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	want := TagSet{"env": "prod", "region": "cn", "integrity": "high"}
	if Digest(obj.Tags) != Digest(want) {
		t.Fatalf("tags = %v, want %v", obj.Tags, want)
	}
	if obj.Digest != Digest(want) {
		t.Fatalf("stored digest %q does not match final tags", obj.Digest)
	}
}

func TestCreateDeniedWhenGrantOnlyCoversRequestedTags(t *testing.T) {
	caller := "mallory"
	requested := TagSet{"env": "prod"}
	// 授权只覆盖请求标签本身的摘要，而非合并后的最终摘要。
	h, _ := newHostWithGrants(caller, Digest(requested))

	if _, err := h.Create(CreateRequest{Caller: caller, Tags: requested}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("err = %v, want ErrForbidden", err)
	}
}

func TestCreateDeniedLockedTagOverride(t *testing.T) {
	caller := "bob"
	h, _ := newHostWithGrants(caller) // 无授权也应先命中合并校验
	_, err := h.Create(CreateRequest{Caller: caller, Tags: TagSet{"integrity": "low"}})
	if !errors.Is(err, ErrLockedTagConflict) {
		t.Fatalf("err = %v, want ErrLockedTagConflict", err)
	}
}

func TestCreateFailurePointsLeaveNothingVisible(t *testing.T) {
	caller := "alice"
	requested := TagSet{"env": "prod"}
	digest := finalDigest(t, requested)

	points := []string{FailCreateObject, FailPutTags, FailCommit}
	for _, point := range points {
		t.Run(point, func(t *testing.T) {
			h, fs := newHostWithGrants(caller, digest)
			fs.Arm(point, false)

			if _, err := h.Create(CreateRequest{Caller: caller, Tags: requested}); !errors.Is(err, ErrInjected) {
				t.Fatalf("err = %v, want ErrInjected", err)
			}
			// 回滚后任何 ID 都不可见：直接检查底层存储无残留。
			for i := 1; i <= 3; i++ {
				id := "obj-" + string(rune('0'+i))
				if _, ok := fs.Inner.Get(id); ok {
					t.Fatalf("object %s visible after failed create at %s", id, point)
				}
			}
		})
	}
}

func TestCreateRollbackFailureIsReported(t *testing.T) {
	caller := "alice"
	requested := TagSet{"env": "prod"}
	h, fs := newHostWithGrants(caller, finalDigest(t, requested))
	fs.Arm(FailPutTags, false)
	fs.Arm(FailDelete, false)

	_, err := h.Create(CreateRequest{Caller: caller, Tags: requested})
	if !errors.Is(err, ErrInjected) || !errors.Is(err, ErrRollbackFailed) {
		t.Fatalf("err = %v, want joined ErrInjected+ErrRollbackFailed", err)
	}
}

func TestExternalReadersNeverSeePartialState(t *testing.T) {
	caller := "alice"
	reader := "carol"
	requested := TagSet{"env": "prod"}
	digest := finalDigest(t, requested)

	grants := NewDigestGrantSet()
	grants.Grant(caller, digest)
	grants.Grant(reader, digest)
	fs := NewFailableStore(NewMemStore())
	h := NewHost(fs, grants, TagSet{"region": "cn"}, TagSet{"integrity": "high"})

	const creates = 50
	var wg sync.WaitGroup
	stop := make(chan struct{})
	violations := make(chan string, creates)

	// 外部读者持续轮询：只允许看到“不存在”或“完整授权状态”。
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			for i := 1; i <= creates; i++ {
				id := "obj-" + itoa(i)
				obj, ok := fs.Get(id)
				if !ok {
					continue
				}
				if obj.State != StateCommitted || obj.Digest != digest || Digest(obj.Tags) != digest {
					violations <- id
				}
			}
		}
	}()

	ids := make([]string, 0, creates)
	for i := 0; i < creates; i++ {
		id, err := h.Create(CreateRequest{Caller: caller, Tags: requested})
		if err != nil {
			t.Fatalf("Create %d: %v", i, err)
		}
		ids = append(ids, id)
	}
	close(stop)
	wg.Wait()
	close(violations)
	for id := range violations {
		t.Fatalf("reader observed partial state for %s", id)
	}

	// 提交后授权读者可见完整状态，未授权读者被拒绝。
	for _, id := range ids {
		if _, err := h.Read(reader, id); err != nil {
			t.Fatalf("authorized Read(%s): %v", id, err)
		}
		if _, err := h.Read("eve", id); !errors.Is(err, ErrForbidden) {
			t.Fatalf("unauthorized Read(%s) err = %v, want ErrForbidden", id, err)
		}
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [8]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
