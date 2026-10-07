package pseq

import (
	"math/rand"
	"testing"
)

// modelDigest 用数组模型重算多项式摘要。
func modelDigest(xs []int64) int64 {
	h := int64(0)
	for _, x := range xs {
		h = (h*Base + Norm(x)) % Mod
	}
	return h
}

// 以下辅助函数在派生新版本后释放旧句柄，模拟“就地演进”的用法，
// 避免不再使用的中间版本占用节点。

func pushBack(v *Version, x int64) *Version  { nv := v.PushBack(x); v.Release(); return nv }
func pushFront(v *Version, x int64) *Version { nv := v.PushFront(x); v.Release(); return nv }
func popFront(v *Version) *Version           { nv := v.PopFront(); v.Release(); return nv }
func popBack(v *Version) *Version            { nv := v.PopBack(); v.Release(); return nv }

func build(xs ...int64) *Version {
	v := New()
	for _, x := range xs {
		v = pushBack(v, x)
	}
	return v
}

func checkVersion(t *testing.T, v *Version, model []int64) {
	t.Helper()
	ev := v.Evidence()
	if ev.Len != len(model) {
		t.Fatalf("len: got %d want %d", ev.Len, len(model))
	}
	if ev.Digest != modelDigest(model) {
		t.Fatalf("digest: got %d want %d (model %v)", ev.Digest, modelDigest(model), model)
	}
	got := v.Elements()
	if len(got) != len(model) {
		t.Fatalf("elements len: got %d want %d", len(got), len(model))
	}
	for i := range got {
		if got[i] != Norm(model[i]) {
			t.Fatalf("elements[%d]: got %d want %d", i, got[i], Norm(model[i]))
		}
	}
	for l := 0; l <= len(model); l++ {
		for r := l; r <= len(model); r++ {
			rev := v.RangeEvidence(l, r)
			if rev.Len != r-l || rev.Digest != modelDigest(model[l:r]) {
				t.Fatalf("range [%d,%d): got %+v want {%d %d}",
					l, r, rev, modelDigest(model[l:r]), r-l)
			}
		}
	}
}

func TestNormNegativeElements(t *testing.T) {
	a := build(-Mod-5, 0)
	a = pushBack(a, -1)
	b := build(Norm(-Mod-5), 0)
	b = pushBack(b, Mod-1)
	defer a.Release()
	defer b.Release()
	if a.Evidence() != b.Evidence() {
		t.Fatalf("normalized evidence mismatch: %+v vs %+v", a.Evidence(), b.Evidence())
	}
	if !EqualContent(a, b) {
		t.Fatal("normalized contents should be equal")
	}
	if got := a.Elements(); got[0] != Norm(-Mod-5) || got[2] != Mod-1 {
		t.Fatalf("unexpected elements %v", got)
	}
}

func TestZeroElementVsEmpty(t *testing.T) {
	empty := New()
	zero := build(0)
	defer empty.Release()
	defer zero.Release()
	if empty.Digest() != zero.Digest() {
		t.Fatalf("digests should coincide for [] and [0]: %d vs %d", empty.Digest(), zero.Digest())
	}
	if empty.Evidence() == zero.Evidence() {
		t.Fatal("length evidence must distinguish [] from [0]")
	}
	if EqualContent(empty, zero) {
		t.Fatal("content must differ despite equal digest")
	}
}

func TestSameSequenceDifferentOpPaths(t *testing.T) {
	// 路径 1：全部右端追加。
	v1 := build(3, 1, 4, 1, 5)
	defer v1.Release()
	// 路径 2：全部左端追加（逆序）。
	v2 := New()
	for _, x := range []int64{5, 1, 4, 1, 3} {
		v2 = pushFront(v2, x)
	}
	defer v2.Release()
	// 路径 3：拼接 + 两端删除。
	left := build(3, 1)
	mid := build(4, 1)
	right := build(5, 9)
	lm := Concat(left, mid)
	cat := Concat(lm, right)
	lm.Release()
	left.Release()
	mid.Release()
	right.Release()
	v3 := popBack(cat)
	defer v3.Release()
	if v1.Evidence() != v2.Evidence() || v1.Evidence() != v3.Evidence() {
		t.Fatalf("same sequence, different evidence: %+v %+v %+v",
			v1.Evidence(), v2.Evidence(), v3.Evidence())
	}
	if !EqualContent(v1, v2) || !EqualContent(v1, v3) {
		t.Fatal("same sequence, content mismatch")
	}
}

func TestModCollisionNotContent(t *testing.T) {
	// [1,0] 与 [0,Base] 摘要同为 Base、长度同为 2，但内容不同。
	a := build(1, 0)
	b := build(0, Base)
	defer a.Release()
	defer b.Release()
	if a.Evidence() != b.Evidence() {
		t.Fatalf("expected colliding evidence, got %+v vs %+v", a.Evidence(), b.Evidence())
	}
	if EqualContent(a, b) {
		t.Fatal("collision must not be treated as equal content")
	}
	if got := b.Elements(); got[1] != Base {
		t.Fatalf("element equal to Base must survive normalization: %v", got)
	}
}

func TestConcatSharedNodesNoMutableAlias(t *testing.T) {
	baseline := LiveNodes()
	base := New()
	model := []int64{}
	for i := int64(0); i < 50; i++ {
		base = pushBack(base, i)
		model = append(model, i)
	}
	// v1、v2 共享 base 的全部底层节点。
	v1 := base.Snapshot()
	v2 := base.PushBack(100)
	cat := Concat(v1, v2)
	catEv := cat.Evidence()
	// 对源版本继续派生与释放，不得影响拼接结果。
	v1b := v1.PushFront(-7)
	v2b := v2.PopBack()
	v1.Release()
	v2.Release()
	base.Release()
	v1b.Release()
	v2b.Release()
	if got := cat.Evidence(); got != catEv {
		t.Fatalf("concat result mutated by aliasing: %+v -> %+v", catEv, got)
	}
	catModel := append(append([]int64{}, model...), model...)
	catModel = append(catModel, 100)
	checkVersion(t, cat, catModel)
	cat.Release()
	if LiveNodes() != baseline {
		t.Fatalf("leaked nodes after all releases: %d live, baseline %d", LiveNodes(), baseline)
	}
}

func TestRepeatedSelfConcat(t *testing.T) {
	baseline := LiveNodes()
	v := build(1, 2, 3)
	c1 := Concat(v, v)
	c2 := Concat(c1, c1)
	c3 := Concat(c2, c2)
	model := []int64{1, 2, 3}
	for i := 0; i < 3; i++ {
		model = append(model, model...)
	}
	checkVersion(t, c3, model)
	// 释放中间版本后，重复拼接结果必须仍然完整。
	v.Release()
	c1.Release()
	c2.Release()
	checkVersion(t, c3, model)
	c3.Release()
	if LiveNodes() != baseline {
		t.Fatalf("leaked nodes: %d live, baseline %d", LiveNodes(), baseline)
	}
}

func TestReleaseReclaimsUnreachableNodes(t *testing.T) {
	baseline := LiveNodes()
	const n = 200
	v := New()
	for i := 0; i < n; i++ {
		v = pushBack(v, int64(i))
	}
	if got := LiveNodes(); got != baseline+n {
		t.Fatalf("live nodes after build: got %d want %d", got, baseline+n)
	}
	snap := v.Snapshot()
	if got := LiveNodes(); got != baseline+n {
		t.Fatalf("snapshot must not allocate: got %d want %d", got, baseline+n)
	}
	v2 := v.PushBack(-1)
	liveAfterPush := LiveNodes()
	if liveAfterPush <= baseline+n {
		t.Fatalf("push should allocate path nodes, live=%d baseline=%d", liveAfterPush, baseline)
	}
	v.Release()
	if got := LiveNodes(); got != liveAfterPush {
		t.Fatalf("releasing v must free nothing while snapshot holds it: got %d want %d", got, liveAfterPush)
	}
	snap.Release()
	v2.Release()
	if got := LiveNodes(); got != baseline {
		t.Fatalf("unreachable nodes not reclaimed: got %d want %d", got, baseline)
	}
}

func TestForkDivergence(t *testing.T) {
	v := build(1, 2, 3)
	a := pushBack(v.Snapshot(), 4)
	b := pushFront(v.Snapshot(), 0)
	defer v.Release()
	defer a.Release()
	defer b.Release()
	checkVersion(t, v, []int64{1, 2, 3})
	checkVersion(t, a, []int64{1, 2, 3, 4})
	checkVersion(t, b, []int64{0, 1, 2, 3})
}

func TestSliceAndRangeEvidence(t *testing.T) {
	v := New()
	model := []int64{}
	for i := int64(0); i < 40; i++ {
		v = pushBack(v, i*i-7)
		model = append(model, i*i-7)
	}
	defer v.Release()
	s := v.Slice(5, 33)
	defer s.Release()
	checkVersion(t, s, model[5:33])
	whole := v.Slice(0, 40)
	defer whole.Release()
	checkVersion(t, whole, model)
	empty := v.Slice(10, 10)
	defer empty.Release()
	checkVersion(t, empty, nil)
	clamped := v.Slice(-5, 100)
	defer clamped.Release()
	checkVersion(t, clamped, model)
}

func TestModelRandomOps(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	baseline := LiveNodes()
	var versions []*Version
	var models [][]int64
	addVersion := func(v *Version, m []int64) {
		if len(versions) < 8 {
			versions = append(versions, v)
			models = append(models, m)
			return
		}
		i := rng.Intn(len(versions))
		versions[i].Release()
		versions[i] = v
		models[i] = m
	}
	versions = append(versions, New())
	models = append(models, nil)

	for step := 0; step < 3000; step++ {
		i := rng.Intn(len(versions))
		switch rng.Intn(9) {
		case 0:
			x := rng.Int63n(2*Mod) - Mod // 覆盖负数与超模元素
			addVersion(versions[i].PushBack(x), append(append([]int64{}, models[i]...), x))
		case 1:
			x := rng.Int63n(2*Mod) - Mod
			addVersion(versions[i].PushFront(x), append([]int64{x}, models[i]...))
		case 2:
			m := models[i]
			nv := versions[i].PopFront()
			if len(m) > 0 {
				m = m[1:]
			}
			addVersion(nv, append([]int64{}, m...))
		case 3:
			m := models[i]
			nv := versions[i].PopBack()
			if len(m) > 0 {
				m = m[:len(m)-1]
			}
			addVersion(nv, append([]int64{}, m...))
		case 4:
			addVersion(versions[i].Snapshot(), append([]int64{}, models[i]...))
		case 5:
			j := rng.Intn(len(versions))
			m := append(append([]int64{}, models[i]...), models[j]...)
			addVersion(Concat(versions[i], versions[j]), m)
		case 6:
			m := models[i]
			l, r := 0, 0
			if len(m) > 0 {
				l = rng.Intn(len(m) + 1)
				r = rng.Intn(len(m) + 1)
				if l > r {
					l, r = r, l
				}
			}
			addVersion(versions[i].Slice(l, r), append([]int64{}, m[l:r]...))
		case 7, 8:
			m := models[i]
			if len(m) == 0 {
				continue
			}
			l := rng.Intn(len(m) + 1)
			r := rng.Intn(len(m) + 1)
			if l > r {
				l, r = r, l
			}
			ev := versions[i].RangeEvidence(l, r)
			if ev.Len != r-l || ev.Digest != modelDigest(m[l:r]) {
				t.Fatalf("step %d range [%d,%d): got %+v want {%d %d}",
					step, l, r, ev, modelDigest(m[l:r]), r-l)
			}
		}
		for k := range versions {
			ev := versions[k].Evidence()
			if ev.Len != len(models[k]) || ev.Digest != modelDigest(models[k]) {
				t.Fatalf("step %d version %d: got %+v, model len %d digest %d",
					step, k, ev, len(models[k]), modelDigest(models[k]))
			}
		}
	}
	for _, v := range versions {
		v.Release()
	}
	if got := LiveNodes(); got != baseline {
		t.Fatalf("leaked nodes after random ops: got %d want %d", got, baseline)
	}
}
