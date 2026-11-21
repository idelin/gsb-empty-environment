package admission

import (
	"reflect"
	"testing"
)

const smallDomainPages = 16

// fakeVerifier 按发布者决定可信级别，模拟宿主的签名与信任策略。
type fakeVerifier struct {
	trusted map[string]bool
}

func (f fakeVerifier) VerifyReceipt(r Receipt) Trust {
	if r.Publisher == "bad" {
		return TrustRejected
	}
	if f.trusted[r.Publisher] {
		return TrustTrusted
	}
	return TrustUntrusted
}

func testShards() []Range {
	// 小地址域测试仍以单个完整分片覆盖全部 48 位空间：分片边界由宿主
	// 提供，单分片是合法且可核验的边界，且让区间收据天然分片对齐。
	return []Range{{0, MaxPage}}
}

func fullPerm(perm uint8) []PermissionRange {
	return []PermissionRange{{Pages: Range{0, MaxPage}, Perm: perm}}
}

func root(name string) []byte { return []byte(name) }

func digest(name string) []byte { return []byte("digest-" + name) }

func baseCheckpoint() Checkpoint {
	return Checkpoint{
		Root:        root("R0"),
		Shards:      testShards(),
		Permissions: fullPerm(PermExec),
		Base: []BaseContent{{
			Pages:  Range{0, MaxPage},
			Digest: digest("base"),
			Trust:  TrustTrusted,
		}},
	}
}

func contentReceipt(pub, old, new string, pages Range,
	entries ...ContentEntry) Receipt {
	if entries == nil {
		entries = []ContentEntry{{Pages: pages, Digest: digest(new)}}
	}
	return Receipt{
		Publisher: pub,
		OldRoot:   root(old),
		NewRoot:   root(new),
		Pages:     pages,
		Kind:      KindContent,
		Content:   entries,
	}
}

func permReceipt(pub, old, new string, pages Range, revoke ...Range) Receipt {
	return Receipt{
		Publisher:  pub,
		OldRoot:    root(old),
		NewRoot:    root(new),
		Pages:      pages,
		Kind:       KindPermission,
		RevokeExec: revoke,
	}
}

func runAdmit(t *testing.T, in AdmissionInput) map[Verdict][]Range {
	t.Helper()
	a, err := Build(in)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	decisions, err := a.Admit()
	if err != nil {
		t.Fatalf("Admit: %v", err)
	}
	ex, fb, un := RangesByVerdict(decisions)
	return map[Verdict][]Range{
		VerdictExecutable: ex,
		VerdictForbidden:  fb,
		VerdictUnknown:    un,
	}
}

func baseInput(receipts []Receipt, heads [][]byte) AdmissionInput {
	return AdmissionInput{
		Checkpoint: baseCheckpoint(),
		Receipts:   receipts,
		Heads:      heads,
		Request:    Range{0, smallDomainPages - 1},
		Verifier:   fakeVerifier{trusted: map[string]bool{"vendor": true}},
	}
}

func assertVerdicts(t *testing.T, got, want map[Verdict][]Range) {
	t.Helper()
	for _, m := range []map[Verdict][]Range{got, want} {
		for v := Verdict(0); v <= VerdictForbidden; v++ {
			if len(m[v]) == 0 {
				m[v] = nil
			}
		}
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}
