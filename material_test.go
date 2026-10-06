package material

import (
	"reflect"
	"strings"
	"testing"
)

func mustCreate(t *testing.T, l *Library, id, summary string) {
	t.Helper()
	if err := l.Create(id, summary); err != nil {
		t.Fatalf("Create(%s): %v", id, err)
	}
}

func mustSign(t *testing.T, l *Library, id string, version int, signer string) {
	t.Helper()
	if err := l.Sign(id, version, signer); err != nil {
		t.Fatalf("Sign(%s@%d, %s): %v", id, version, signer, err)
	}
}

// 内容改动清空新版签收，但旧版本及其签收记录完整保留。
func TestUpdateClearsNewVersionSignersKeepsOld(t *testing.T) {
	l := NewLibrary()
	mustCreate(t, l, "ev1", "初始摘要")
	mustSign(t, l, "ev1", 1, "alice")
	mustSign(t, l, "ev1", 1, "bob")

	next, err := l.Update("ev1", "修订摘要")
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if next != 2 {
		t.Fatalf("next version = %d, want 2", next)
	}

	view, ok := l.Snapshot("ev1")
	if !ok {
		t.Fatal("ev1 missing")
	}
	if view.Current != 2 {
		t.Fatalf("current = %d, want 2", view.Current)
	}
	if got := view.Versions[2].Signers; len(got) != 0 {
		t.Fatalf("v2 signers = %v, want empty", got)
	}
	if got := view.Versions[1].Signers; !reflect.DeepEqual(got, []string{"alice", "bob"}) {
		t.Fatalf("v1 signers = %v, want [alice bob]", got)
	}
	if view.Versions[1].Summary != "初始摘要" {
		t.Fatalf("v1 summary = %q, want 初始摘要", view.Versions[1].Summary)
	}
}

// 签收登记与撤回均按版本生效。
func TestSignAndWithdrawPerVersion(t *testing.T) {
	l := NewLibrary()
	mustCreate(t, l, "ev1", "v1")
	mustSign(t, l, "ev1", 1, "alice")
	if _, err := l.Update("ev1", "v2"); err != nil {
		t.Fatal(err)
	}
	mustSign(t, l, "ev1", 2, "carol")

	if err := l.Withdraw("ev1", 1, "alice"); err != nil {
		t.Fatalf("Withdraw: %v", err)
	}
	view, _ := l.Snapshot("ev1")
	if got := view.Versions[1].Signers; len(got) != 0 {
		t.Fatalf("v1 signers after withdraw = %v, want empty", got)
	}
	if got := view.Versions[2].Signers; !reflect.DeepEqual(got, []string{"carol"}) {
		t.Fatalf("v2 signers = %v, want [carol]", got)
	}
	if err := l.Withdraw("ev1", 1, "alice"); err != ErrSignerNotFound {
		t.Fatalf("double withdraw err = %v, want ErrSignerNotFound", err)
	}
	if err := l.Sign("ev1", 99, "dave"); err == nil {
		t.Fatal("sign on missing version should fail")
	}
}

// 冻结集合内的批量发布成功，发布后状态可查。
func TestPublishSuccess(t *testing.T) {
	l := NewLibrary()
	mustCreate(t, l, "a", "证据A")
	mustCreate(t, l, "b", "证据B")
	mustSign(t, l, "a", 1, "alice")
	mustSign(t, l, "b", 1, "bob")

	frozen, err := l.Freeze("a", "b")
	if err != nil {
		t.Fatal(err)
	}
	res, err := l.Publish(frozen, "a", "b")
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if len(res.Published) != 2 {
		t.Fatalf("published = %v, want 2 refs", res.Published)
	}
	if !l.IsPublished("a", 1) || !l.IsPublished("b", 1) {
		t.Fatal("both evidences should be published")
	}
}

// 冻结后内容更新导致版本推进，发布失败且原子：任何证据都不被发布。
func TestPublishAtomicFailureAfterUpdate(t *testing.T) {
	l := NewLibrary()
	mustCreate(t, l, "a", "证据A")
	mustCreate(t, l, "b", "证据B")
	mustSign(t, l, "a", 1, "alice")
	mustSign(t, l, "b", 1, "bob")

	frozen, err := l.Freeze("a", "b")
	if err != nil {
		t.Fatal(err)
	}
	// 冻结后 b 的内容被改动，推进到 v2（签收被清空）。
	if _, err := l.Update("b", "证据B-修订"); err != nil {
		t.Fatal(err)
	}

	if _, err := l.Publish(frozen, "a", "b"); err == nil {
		t.Fatal("publish should fail when a member advanced past the frozen version")
	} else if !strings.Contains(err.Error(), "b") {
		t.Fatalf("error should name the offending evidence, got: %v", err)
	}
	if l.IsPublished("a", 1) || l.IsPublished("b", 1) || l.IsPublished("b", 2) {
		t.Fatal("atomic failure: nothing may be published")
	}
}

// 撤回使签收人集合为空时，发布同样原子失败。
func TestPublishAtomicFailureAfterWithdraw(t *testing.T) {
	l := NewLibrary()
	mustCreate(t, l, "a", "证据A")
	mustCreate(t, l, "b", "证据B")
	mustSign(t, l, "a", 1, "alice")
	mustSign(t, l, "b", 1, "bob")

	frozen, err := l.Freeze("a", "b")
	if err != nil {
		t.Fatal(err)
	}
	if err := l.Withdraw("b", 1, "bob"); err != nil {
		t.Fatal(err)
	}

	if _, err := l.Publish(frozen, "a", "b"); err == nil {
		t.Fatal("publish should fail when a signer set became empty")
	}
	if l.IsPublished("a", 1) {
		t.Fatal("atomic failure: a must not be published either")
	}
}

// 依赖必须落在同一冻结集合内：依赖更新后指向新版本即构成新旧混用，发布失败。
func TestPublishRejectsMixedDependencyVersions(t *testing.T) {
	l := NewLibrary()
	mustCreate(t, l, "report", "报告")
	mustCreate(t, l, "attach", "附件")
	mustSign(t, l, "report", 1, "alice")
	mustSign(t, l, "attach", 1, "bob")

	// report@1 依赖 attach@1。
	if err := l.Graph().AddDependency(Ref{"report", 1}, Ref{"attach", 1}); err != nil {
		t.Fatal(err)
	}

	frozen, err := l.Freeze("report", "attach")
	if err != nil {
		t.Fatal(err)
	}

	// 附件更新到 v2 并获得新签收；report 的依赖仍指向旧的 attach@1。
	if _, err := l.Update("attach", "附件-修订"); err != nil {
		t.Fatal(err)
	}
	mustSign(t, l, "attach", 2, "carol")

	if _, err := l.Publish(frozen, "report", "attach"); err == nil {
		t.Fatal("publish must fail: dependency points at old version while frozen set moved on")
	}
	if l.IsPublished("report", 1) || l.IsPublished("attach", 2) {
		t.Fatal("atomic failure: nothing may be published")
	}
}

// 依赖指向冻结集合之外的证据时，版本图核对失败。
func TestCheckFrozenRejectsDependencyOutsideSet(t *testing.T) {
	l := NewLibrary()
	mustCreate(t, l, "report", "报告")
	mustCreate(t, l, "attach", "附件")
	mustSign(t, l, "report", 1, "alice")
	mustSign(t, l, "attach", 1, "bob")
	if err := l.Graph().AddDependency(Ref{"report", 1}, Ref{"attach", 1}); err != nil {
		t.Fatal(err)
	}

	// 冻结集合只含 report，遗漏其依赖 attach。
	frozen, err := l.Freeze("report")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := l.Publish(frozen, "report"); err == nil {
		t.Fatal("publish must fail: dependency attach not in frozen set")
	}
	if l.IsPublished("report", 1) {
		t.Fatal("atomic failure: report must not be published")
	}
}

// 依赖与冻结集合一致时，版本图核对通过，发布成功。
func TestPublishWithConsistentDependencies(t *testing.T) {
	l := NewLibrary()
	mustCreate(t, l, "report", "报告")
	mustCreate(t, l, "attach", "附件")
	mustSign(t, l, "report", 1, "alice")
	mustSign(t, l, "attach", 1, "bob")
	if err := l.Graph().AddDependency(Ref{"report", 1}, Ref{"attach", 1}); err != nil {
		t.Fatal(err)
	}

	frozen, err := l.Freeze("report", "attach")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := l.Publish(frozen, "report", "attach"); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if !l.IsPublished("report", 1) || !l.IsPublished("attach", 1) {
		t.Fatal("both should be published")
	}
}

// 版本图事件流可重放出签收人集合，与材料库状态一致，用于交叉核对撤回。
func TestGraphEventReplayMatchesLibrary(t *testing.T) {
	l := NewLibrary()
	mustCreate(t, l, "ev1", "v1")
	mustSign(t, l, "ev1", 1, "alice")
	mustSign(t, l, "ev1", 1, "bob")
	if err := l.Withdraw("ev1", 1, "alice"); err != nil {
		t.Fatal(err)
	}

	got := l.Graph().SignerOf(Ref{"ev1", 1})
	if !reflect.DeepEqual(got, []string{"bob"}) {
		t.Fatalf("replayed signers = %v, want [bob]", got)
	}
	view, _ := l.Snapshot("ev1")
	if !reflect.DeepEqual(view.Versions[1].Signers, got) {
		t.Fatalf("library signers %v != graph replay %v", view.Versions[1].Signers, got)
	}
}
