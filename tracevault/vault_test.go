package tracevault

import (
	"strings"
	"sync"
	"testing"
	"time"
)

func testKeys(t *testing.T) *Keys {
	t.Helper()
	master := make([]byte, 32)
	for i := range master {
		master[i] = byte(i + 1)
	}
	return DeriveKeys(master)
}

func TestPseudonymIsTimeVarying(t *testing.T) {
	k := testKeys(t)
	p1 := k.pseudonymFor("user-001", 100)
	p2 := k.pseudonymFor("user-001", 100)
	p3 := k.pseudonymFor("user-001", 101)
	p4 := k.pseudonymFor("user-002", 100)

	if p1 != p2 {
		t.Fatal("same entity and bucket must yield a stable pseudonym")
	}
	if p1 == p3 {
		t.Fatal("pseudonym must rotate across time buckets")
	}
	if p1 == p4 {
		t.Fatal("different entities must not share a pseudonym")
	}
	if strings.Contains(p1, "user-001") {
		t.Fatal("pseudonym must not leak the real id")
	}
}

func TestGridTokenSaltedAndTimeVarying(t *testing.T) {
	k1 := testKeys(t)
	otherMaster := make([]byte, 32)
	for i := range otherMaster {
		otherMaster[i] = byte(255 - i)
	}
	k2 := DeriveKeys(otherMaster)

	cell := CellFor(31.2304, 121.4737, 0.01)
	tok1 := k1.gridToken(cell, 7)
	if tok1 != k1.gridToken(cell, 7) {
		t.Fatal("grid token must be deterministic")
	}
	if tok1 == k1.gridToken(cell, 8) {
		t.Fatal("grid token must rotate across time buckets")
	}
	if tok1 == k2.gridToken(cell, 7) {
		t.Fatal("grid token must depend on the salt key")
	}
	if tok1 == k1.gridToken(CellFor(31.24, 121.48, 0.01), 7) {
		t.Fatal("different cells must not share a token")
	}
}

func TestInsertValidation(t *testing.T) {
	v := NewVault(testKeys(t), DefaultBucketSize, 0.01)
	if err := v.Insert("", 31.2, 121.4, 1000); err == nil {
		t.Fatal("empty entity id must be rejected")
	}
	if err := v.Insert("u", 91, 121.4, 1000); err == nil {
		t.Fatal("out-of-range latitude must be rejected")
	}
	if err := v.Insert("u", 31.2, 181, 1000); err == nil {
		t.Fatal("out-of-range longitude must be rejected")
	}
}

func TestHeatmapAggregationAndKAnonymity(t *testing.T) {
	v := NewVault(testKeys(t), DefaultBucketSize, 0.01)
	base := int64(1_700_000_000)
	bucket := v.BucketOf(base)

	// 3 个用户落在同一网格，1 个用户落在孤立网格。
	for i, id := range []string{"u1", "u2", "u3"} {
		if err := v.Insert(id, 31.2301+float64(i)*0.0001, 121.4731, base+int64(i)); err != nil {
			t.Fatal(err)
		}
	}
	if err := v.Insert("lonely", 39.9042, 116.4074, base); err != nil {
		t.Fatal(err)
	}

	heat := v.Heatmap(bucket, 1)
	if len(heat) != 2 {
		t.Fatalf("expected 2 grid cells, got %d", len(heat))
	}
	total := 0
	for _, n := range heat {
		total += n
	}
	if total != 4 {
		t.Fatalf("expected 4 observations in aggregate, got %d", total)
	}

	// k=2 匿名抑制：孤立网格必须被过滤。
	heatK := v.Heatmap(bucket, 2)
	if len(heatK) != 1 {
		t.Fatalf("k-anonymity must suppress sparse cells, got %d cells", len(heatK))
	}
	for token := range heatK {
		if strings.Contains(token, "lonely") || strings.Contains(token, "u1") {
			t.Fatal("heatmap must not leak entity identifiers")
		}
	}
}

func TestRevealWithValidCredential(t *testing.T) {
	keys := testKeys(t)
	v := NewVault(keys, DefaultBucketSize, 0.01)
	base := int64(1_700_000_000)
	b0 := v.BucketOf(base)

	type obs struct {
		id        string
		lat, lng  float64
		timestamp int64
	}
	target := []obs{
		{"alice", 31.2304, 121.4737, base + 10},
		{"alice", 31.2314, 121.4747, base + 20},
		{"alice", 31.2324, 121.4757, base + int64(DefaultBucketSize) + 5}, // 下一个时间桶
	}
	others := []obs{
		{"bob", 31.2304, 121.4737, base + 11},
		{"carol", 31.9999, 121.9999, base + 12},
	}
	for _, o := range append(append([]obs{}, target...), others...) {
		if err := v.Insert(o.id, o.lat, o.lng, o.timestamp); err != nil {
			t.Fatal(err)
		}
	}

	cred, err := IssueCredential(keys.AuditKey(), "court-order-2026-001", "alice", b0, b0+1, time.Now().Unix()+3600)
	if err != nil {
		t.Fatal(err)
	}
	pts, err := v.Reveal(cred)
	if err != nil {
		t.Fatalf("reveal failed: %v", err)
	}
	if len(pts) != len(target) {
		t.Fatalf("expected %d points, got %d", len(target), len(pts))
	}
	for i, pt := range pts {
		if pt.EntityID != "alice" {
			t.Fatalf("point %d belongs to %q, want alice", i, pt.EntityID)
		}
		if pt.Timestamp != target[i].timestamp {
			t.Fatalf("point %d timestamp = %d, want %d", i, pt.Timestamp, target[i].timestamp)
		}
		if pt.Lat != target[i].lat || pt.Lng != target[i].lng {
			t.Fatalf("point %d = (%v, %v), want (%v, %v)", i, pt.Lat, pt.Lng, target[i].lat, target[i].lng)
		}
	}

	// 审计日志必须记录本次定向还原。
	entries := v.AuditLog().Entries()
	if len(entries) != 1 || entries[0].Action != "reveal" || entries[0].Records != 3 {
		t.Fatalf("audit log missing reveal entry: %+v", entries)
	}
	if err := v.AuditLog().Verify(); err != nil {
		t.Fatalf("audit chain broken: %v", err)
	}
}

func TestRevealScopeIsLimitedToCredentialRange(t *testing.T) {
	keys := testKeys(t)
	v := NewVault(keys, DefaultBucketSize, 0.01)
	base := int64(1_700_000_000)
	b0 := v.BucketOf(base)

	v.Insert("alice", 31.0, 121.0, base)                     // 桶 b0
	v.Insert("alice", 31.1, 121.1, base+DefaultBucketSize)   // 桶 b0+1
	v.Insert("alice", 31.2, 121.2, base+2*DefaultBucketSize) // 桶 b0+2

	// 凭证只覆盖 [b0, b0+1]，b0+2 的点不得被还原。
	cred, err := IssueCredential(keys.AuditKey(), "procuratorate", "alice", b0, b0+1, time.Now().Unix()+3600)
	if err != nil {
		t.Fatal(err)
	}
	pts, err := v.Reveal(cred)
	if err != nil {
		t.Fatal(err)
	}
	if len(pts) != 2 {
		t.Fatalf("reveal must be limited to the authorized range, got %d points", len(pts))
	}
}

func TestRevealRejectsInvalidCredentials(t *testing.T) {
	keys := testKeys(t)
	v := NewVault(keys, DefaultBucketSize, 0.01)
	base := int64(1_700_000_000)
	b0 := v.BucketOf(base)
	v.Insert("alice", 31.0, 121.0, base)

	valid, err := IssueCredential(keys.AuditKey(), "court", "alice", b0, b0, time.Now().Unix()+3600)
	if err != nil {
		t.Fatal(err)
	}

	// 1) 篡改目标实体后 MAC 失效。
	forged := *valid
	forged.EntityID = "mallory"
	if _, err := v.Reveal(&forged); err == nil {
		t.Fatal("forged credential must be rejected")
	}

	// 2) 过期凭证。
	expired, err := IssueCredential(keys.AuditKey(), "court", "alice", b0, b0, time.Now().Unix()-10)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := v.Reveal(expired); err == nil {
		t.Fatal("expired credential must be rejected")
	}

	// 3) 错误的审计密钥签发的凭证。
	wrongKey := make([]byte, 32)
	bad, err := IssueCredential(wrongKey, "court", "alice", b0, b0, time.Now().Unix()+3600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := v.Reveal(bad); err == nil {
		t.Fatal("credential signed with wrong key must be rejected")
	}

	// 所有拒绝也必须留痕。
	entries := v.AuditLog().Entries()
	if len(entries) != 3 {
		t.Fatalf("expected 3 denied audit entries, got %d", len(entries))
	}
	for _, e := range entries {
		if e.Action != "reveal-denied" {
			t.Fatalf("expected reveal-denied, got %q", e.Action)
		}
	}
	if err := v.AuditLog().Verify(); err != nil {
		t.Fatalf("audit chain broken: %v", err)
	}
}

func TestAuditLogDetectsTampering(t *testing.T) {
	keys := testKeys(t)
	v := NewVault(keys, DefaultBucketSize, 0.01)
	base := int64(1_700_000_000)
	b0 := v.BucketOf(base)
	v.Insert("alice", 31.0, 121.0, base)

	cred, _ := IssueCredential(keys.AuditKey(), "court", "alice", b0, b0, time.Now().Unix()+3600)
	if _, err := v.Reveal(cred); err != nil {
		t.Fatal(err)
	}
	if err := v.AuditLog().Verify(); err != nil {
		t.Fatalf("fresh audit log must verify: %v", err)
	}

	// 篡改日志内容（例如抹掉记录数）后必须被检出。
	log := v.AuditLog()
	log.entries[0].Records = 0
	if err := log.Verify(); err == nil {
		t.Fatal("tampered audit log must fail verification")
	}
}

func TestConcurrentInserts(t *testing.T) {
	v := NewVault(testKeys(t), DefaultBucketSize, 0.01)
	base := int64(1_700_000_000)
	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				id := strings.Repeat("u", 1) + string(rune('a'+w)) + "-" + string(rune('0'+i%10))
				if err := v.Insert(id, 31.0+float64(i%50)*0.001, 121.0, base+int64(i)); err != nil {
					t.Error(err)
					return
				}
			}
		}(w)
	}
	wg.Wait()
	total := 0
	for _, n := range v.Heatmap(v.BucketOf(base), 1) {
		total += n
	}
	if total != 1600 {
		t.Fatalf("expected 1600 observations, got %d", total)
	}
}
