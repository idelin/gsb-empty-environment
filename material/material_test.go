package material

import "testing"

func mustAdd(t *testing.T, l *Library, id string, d Digest) {
	t.Helper()
	if err := l.AddEvidence(id, d); err != nil {
		t.Fatalf("AddEvidence(%s): %v", id, err)
	}
}

func mustSign(t *testing.T, l *Library, id string, v int, signer string) {
	t.Helper()
	if err := l.Sign(id, v, signer); err != nil {
		t.Fatalf("Sign(%s@v%d, %s): %v", id, v, signer, err)
	}
}

// 内容改动：新版本签收清空，旧版本记录（摘要+签收人）完整保留。
func TestUpdateContentClearsNewSignersKeepsOld(t *testing.T) {
	l := NewLibrary()
	mustAdd(t, l, "ev-a", "digest-v1")
	mustSign(t, l, "ev-a", 1, "alice")
	mustSign(t, l, "ev-a", 1, "bob")

	ref, err := l.UpdateContent("ev-a", "digest-v2")
	if err != nil {
		t.Fatalf("UpdateContent: %v", err)
	}
	if ref != (Ref{EvidenceID: "ev-a", Version: 2}) {
		t.Fatalf("unexpected new ref %v", ref)
	}

	old, err := l.Record("ev-a", 1)
	if err != nil {
		t.Fatalf("Record v1: %v", err)
	}
	if old.Digest != "digest-v1" || len(old.Signers) != 2 {
		t.Fatalf("old record not preserved: %+v", old)
	}

	cur, err := l.Record("ev-a", 2)
	if err != nil {
		t.Fatalf("Record v2: %v", err)
	}
	if cur.Digest != "digest-v2" || cur.Signed() {
		t.Fatalf("new version should start unsigned: %+v", cur)
	}
}

// 撤回版本拒绝签收，旧签收记录仍可读。
func TestWithdrawRejectsSigning(t *testing.T) {
	l := NewLibrary()
	mustAdd(t, l, "ev-a", "d1")
	mustSign(t, l, "ev-a", 1, "alice")
	if err := l.Withdraw("ev-a", 1); err != nil {
		t.Fatalf("Withdraw: %v", err)
	}
	if err := l.Sign("ev-a", 1, "bob"); err == nil {
		t.Fatal("signing withdrawn version should fail")
	}
	rec, _ := l.Record("ev-a", 1)
	if !rec.Withdrawn || len(rec.Signers) != 1 {
		t.Fatalf("withdrawn record should keep old signers: %+v", rec)
	}
}

// 版本图核对依赖更新：钉住旧版的依赖边被检出。
func TestGraphDependencyUpdates(t *testing.T) {
	l := NewLibrary()
	mustAdd(t, l, "ev-a", "a1")
	mustAdd(t, l, "ev-b", "b1")
	if err := l.SetDependencies("ev-a", 1, []Ref{{"ev-b", 1}}); err != nil {
		t.Fatal(err)
	}
	if got := l.Graph().DependencyUpdates(); len(got) != 0 {
		t.Fatalf("no updates expected, got %v", got)
	}

	if _, err := l.UpdateContent("ev-b", "b2"); err != nil {
		t.Fatal(err)
	}
	ups := l.Graph().DependencyUpdates()
	if len(ups) != 1 {
		t.Fatalf("expected 1 update edge, got %v", ups)
	}
	want := UpdateEdge{Item: Ref{"ev-a", 1}, Dep: Ref{"ev-b", 1}, Latest: 2}
	if ups[0] != want {
		t.Fatalf("got %+v, want %+v", ups[0], want)
	}
}

// 版本图核对撤回：指向已撤回版本的依赖边被检出。
func TestGraphWithdrawnDeps(t *testing.T) {
	l := NewLibrary()
	mustAdd(t, l, "ev-a", "a1")
	mustAdd(t, l, "ev-b", "b1")
	if err := l.SetDependencies("ev-a", 1, []Ref{{"ev-b", 1}}); err != nil {
		t.Fatal(err)
	}
	if err := l.Withdraw("ev-b", 1); err != nil {
		t.Fatal(err)
	}
	w := l.Graph().WithdrawnDeps()
	if len(w) != 1 || w[0].Dep != (Ref{"ev-b", 1}) {
		t.Fatalf("expected withdrawn dep ev-b@v1, got %v", w)
	}
}

// 批量发布成功：全部依赖落在同一冻结集合内。
func TestPublishFrozenSetOK(t *testing.T) {
	l := NewLibrary()
	mustAdd(t, l, "ev-a", "a1")
	mustAdd(t, l, "ev-b", "b1")
	mustSign(t, l, "ev-a", 1, "alice")
	mustSign(t, l, "ev-b", 1, "bob")
	if err := l.SetDependencies("ev-a", 1, []Ref{{"ev-b", 1}}); err != nil {
		t.Fatal(err)
	}
	batch := []Ref{{"ev-a", 1}, {"ev-b", 1}}
	if err := l.Publish(batch); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	for _, r := range batch {
		if ok, _ := l.Published(r.EvidenceID, r.Version); !ok {
			t.Fatalf("%v should be published", r)
		}
	}
}

// 混用新旧签收：ev-a 钉住 ev-b@v1，批次却带 ev-b@v2 → 原子失败，
// 批次内任何版本都不得被置为已发布。
func TestPublishMixedVersionsFailsAtomically(t *testing.T) {
	l := NewLibrary()
	mustAdd(t, l, "ev-a", "a1")
	mustAdd(t, l, "ev-b", "b1")
	if err := l.SetDependencies("ev-a", 1, []Ref{{"ev-b", 1}}); err != nil {
		t.Fatal(err)
	}
	if _, err := l.UpdateContent("ev-b", "b2"); err != nil {
		t.Fatal(err)
	}
	mustSign(t, l, "ev-a", 1, "alice")
	mustSign(t, l, "ev-b", 2, "bob")

	err := l.Publish([]Ref{{"ev-a", 1}, {"ev-b", 2}})
	if err == nil {
		t.Fatal("mixing old dep with new sign-off must fail")
	}
	for _, r := range []Ref{{"ev-a", 1}, {"ev-b", 2}} {
		if ok, _ := l.Published(r.EvidenceID, r.Version); ok {
			t.Fatalf("%v must not be published after atomic failure", r)
		}
	}
}

// 批次中任一版本未签收或已撤回 → 整体原子失败。
func TestPublishUnsignedOrWithdrawnFailsAtomically(t *testing.T) {
	l := NewLibrary()
	mustAdd(t, l, "ev-a", "a1")
	mustAdd(t, l, "ev-b", "b1")
	mustSign(t, l, "ev-a", 1, "alice") // ev-b 未签收

	if err := l.Publish([]Ref{{"ev-a", 1}, {"ev-b", 1}}); err == nil {
		t.Fatal("unsigned member must fail the batch")
	}
	if ok, _ := l.Published("ev-a", 1); ok {
		t.Fatal("ev-a@v1 must not be published after atomic failure")
	}

	mustSign(t, l, "ev-b", 1, "bob")
	if err := l.Withdraw("ev-b", 1); err != nil {
		t.Fatal(err)
	}
	if err := l.Publish([]Ref{{"ev-a", 1}, {"ev-b", 1}}); err == nil {
		t.Fatal("withdrawn member must fail the batch")
	}
	if ok, _ := l.Published("ev-a", 1); ok {
		t.Fatal("ev-a@v1 must not be published after atomic failure")
	}
}
