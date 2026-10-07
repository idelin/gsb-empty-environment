package snapscan

import (
	"fmt"
	"reflect"
	"testing"
)

func putKeys(s *Store, n int) {
	for i := 1; i <= n; i++ {
		key := fmt.Sprintf("k%02d", i)
		s.Put(key, "v"+key)
	}
}

func grantKeys(p *PermStore, n int) {
	for i := 1; i <= n; i++ {
		p.Grant(fmt.Sprintf("k%02d", i))
	}
}

func recordKeys(recs []Record) []string {
	out := make([]string, len(recs))
	for i, r := range recs {
		out[i] = r.Key
	}
	return out
}

// 数据快照固定：翻页期间的写、删、新增都不影响后续页。
func TestSnapshotFixedAcrossPages(t *testing.T) {
	store := NewStore()
	putKeys(store, 6)
	perms := NewPermStore()
	grantKeys(perms, 6)
	sc := NewScanner(store, perms)

	page1, cur, err := sc.NextPage(nil, 2)
	if err != nil {
		t.Fatal(err)
	}
	if got := recordKeys(page1); !reflect.DeepEqual(got, []string{"k01", "k02"}) {
		t.Fatalf("page1 = %v", got)
	}

	// 快照固定后变更数据：改旧值、删未扫键、新增键。
	store.Put("k02", "modified")
	store.Delete("k03")
	store.Put("k00", "new")
	store.Put("k07", "new")
	perms.Grant("k00")
	perms.Grant("k07")

	var got []string
	for cur != nil {
		var page []Record
		page, cur, err = sc.NextPage(cur, 2)
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, recordKeys(page)...)
	}
	want := []string{"k03", "k04", "k05", "k06"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("subsequent pages = %v, want %v", got, want)
	}
	// 快照中的旧值不受后续写入影响。
	for _, r := range sc.Delivered() {
		if r.Key == "k02" && r.Value != "vk02" {
			t.Fatalf("k02 delivered as %q, want snapshot value %q", r.Value, "vk02")
		}
		if r.Key == "k00" || r.Key == "k07" {
			t.Fatalf("post-snapshot key %s leaked into delivery", r.Key)
		}
	}
}

// 撤权后未交付项不得泄漏；已交付项不撤回。
func TestRevocationSkipsUndelivered(t *testing.T) {
	store := NewStore()
	putKeys(store, 6)
	perms := NewPermStore()
	grantKeys(perms, 6)
	sc := NewScanner(store, perms)

	page1, cur, err := sc.NextPage(nil, 2)
	if err != nil {
		t.Fatal(err)
	}
	if got := recordKeys(page1); !reflect.DeepEqual(got, []string{"k01", "k02"}) {
		t.Fatalf("page1 = %v", got)
	}

	// k03、k04 尚未交付即被撤权；k02 已交付后才撤权。
	perms.Revoke("k03")
	perms.Revoke("k04")
	perms.Revoke("k02")

	page2, cur, err := sc.NextPage(cur, 10)
	if err != nil {
		t.Fatal(err)
	}
	if cur != nil {
		t.Fatalf("expected scan to finish, got cursor %+v", cur)
	}
	if got := recordKeys(page2); !reflect.DeepEqual(got, []string{"k05", "k06"}) {
		t.Fatalf("page2 = %v, want [k05 k06]", got)
	}
	if got := sc.Skipped(); !reflect.DeepEqual(got, []string{"k03", "k04"}) {
		t.Fatalf("skipped = %v, want [k03 k04]", got)
	}
	// 已交付的 k02 不撤回。
	if got := recordKeys(sc.Delivered()); !reflect.DeepEqual(got, []string{"k01", "k02", "k05", "k06"}) {
		t.Fatalf("delivered = %v", got)
	}
}

// 游标记录最后扫描键而非最后交付键：被拒绝的键同样推进游标，
// 之后重新授权也不会补发。
func TestCursorUsesLastScannedKey(t *testing.T) {
	store := NewStore()
	putKeys(store, 5)
	perms := NewPermStore()
	grantKeys(perms, 5)
	perms.Revoke("k03")
	sc := NewScanner(store, perms)
	sc.MaxScanPerPage = 3

	page1, cur, err := sc.NextPage(nil, 10)
	if err != nil {
		t.Fatal(err)
	}
	// 扫描预算 3 被 k01、k02、k03 耗尽，k03 被拒但游标仍推进到 k03。
	if got := recordKeys(page1); !reflect.DeepEqual(got, []string{"k01", "k02"}) {
		t.Fatalf("page1 = %v", got)
	}
	if cur == nil || cur.LastScannedKey != "k03" {
		t.Fatalf("cursor = %+v, want LastScannedKey=k03", cur)
	}

	// 重新授权 k03：它已被扫描跳过，不会补发。
	perms.Grant("k03")
	page2, cur, err := sc.NextPage(cur, 10)
	if err != nil {
		t.Fatal(err)
	}
	if cur != nil {
		t.Fatalf("expected scan to finish, got cursor %+v", cur)
	}
	if got := recordKeys(page2); !reflect.DeepEqual(got, []string{"k04", "k05"}) {
		t.Fatalf("page2 = %v", got)
	}
	if got := recordKeys(sc.Delivered()); !reflect.DeepEqual(got, []string{"k01", "k02", "k04", "k05"}) {
		t.Fatalf("delivered = %v", got)
	}
	if got := sc.Skipped(); !reflect.DeepEqual(got, []string{"k03"}) {
		t.Fatalf("skipped = %v", got)
	}
}

// 扫描预算被无权限键耗尽时返回空页与非 nil 游标，调用方可继续。
func TestEmptyPageWhenBudgetExhaustedByDenies(t *testing.T) {
	store := NewStore()
	putKeys(store, 4)
	perms := NewPermStore()
	perms.Grant("k04")
	sc := NewScanner(store, perms)
	sc.MaxScanPerPage = 3

	page, cur, err := sc.NextPage(nil, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(page) != 0 || cur == nil {
		t.Fatalf("page = %v, cursor = %+v; want empty page with live cursor", page, cur)
	}
	page, cur, err = sc.NextPage(cur, 2)
	if err != nil {
		t.Fatal(err)
	}
	if got := recordKeys(page); !reflect.DeepEqual(got, []string{"k04"}) {
		t.Fatalf("page2 = %v, want [k04]", got)
	}
	if cur != nil {
		t.Fatalf("expected scan to finish, got cursor %+v", cur)
	}
}

// 动态权限模型核对：把每次翻页调用时的权限状态记录下来作为模型，
// 验证每个键的交付/跳过恰好等于其被扫描那一页时的权限，
// 且分页完整、不重复、不越界。
func TestDynamicPermissionModel(t *testing.T) {
	const n = 24
	store := NewStore()
	putKeys(store, n)
	perms := NewPermStore()
	grantKeys(perms, n)
	sc := NewScanner(store, perms)
	sc.MaxScanPerPage = 5

	snap := store.Pin() // 之后不再改数据，测试自有快照与扫描器快照一致
	snapKeys := recordKeys(store.ScanAfter(snap, "", n+1))

	// 每次翻页前执行的权限变更（含对已交付键撤权、对已跳过键重新授权）。
	permOps := map[int][]func(){
		1: {func() { perms.Revoke("k05") }},
		2: {func() { perms.Revoke("k07") }, func() { perms.Revoke("k08") }},
		3: {func() { perms.Revoke("k02") }}, // k02 已交付：不得撤回
		4: {func() { perms.Grant("k05") }},  // k05 已跳过：不得补发
		5: {func() { perms.Revoke("k20") }},
	}

	// 模型：第 i 次调用期间有效的权限集合。
	var modelPerms []map[string]bool
	snapshotPerms := func() map[string]bool {
		m := make(map[string]bool, n)
		for _, k := range snapKeys {
			if perms.Allow(k) {
				m[k] = true
			}
		}
		return m
	}

	const pageSize = 4
	var delivered []string
	var scannedAtCall = make(map[string]int) // 键 -> 被扫描的调用序号
	prev := ""
	call := 0
	var cur *Cursor
	for {
		if ops, ok := permOps[call]; ok {
			for _, op := range ops {
				op()
			}
		}
		modelPerms = append(modelPerms, snapshotPerms())

		page, next, err := sc.NextPage(cur, pageSize)
		if err != nil {
			t.Fatal(err)
		}
		if len(page) > pageSize {
			t.Fatalf("call %d delivered %d items > pageSize %d", call, len(page), pageSize)
		}
		hi := "z" // 快照已耗尽时，本次调用扫到末尾
		if next != nil {
			hi = next.LastScannedKey
		}
		for _, k := range snapKeys {
			if k > prev && k <= hi {
				if _, dup := scannedAtCall[k]; dup {
					t.Fatalf("key %s scanned twice", k)
				}
				scannedAtCall[k] = call
			}
		}
		delivered = append(delivered, recordKeys(page)...)
		prev = hi
		call++
		if next == nil {
			break
		}
		cur = next
	}

	// 分页完整性：每个快照键恰好被扫描一次。
	if len(scannedAtCall) != len(snapKeys) {
		t.Fatalf("scanned %d keys, want %d", len(scannedAtCall), len(snapKeys))
	}

	// 交付集合 == 模型中扫描时刻被授权的键集合。
	var want []string
	for _, k := range snapKeys {
		if modelPerms[scannedAtCall[k]][k] {
			want = append(want, k)
		}
	}
	if !reflect.DeepEqual(delivered, want) {
		t.Fatalf("delivered = %v, want %v", delivered, want)
	}

	// 跳过集合 == 模型中扫描时刻被拒绝的键集合，且与交付无交集。
	skipSet := make(map[string]bool)
	for _, k := range sc.Skipped() {
		skipSet[k] = true
		if modelPerms[scannedAtCall[k]][k] {
			t.Fatalf("key %s skipped despite being allowed at scan time", k)
		}
	}
	for _, k := range delivered {
		if skipSet[k] {
			t.Fatalf("key %s both delivered and skipped", k)
		}
	}
	if got, want := len(delivered)+len(sc.Skipped()), len(snapKeys); got != want {
		t.Fatalf("delivered+skipped = %d, want %d", got, want)
	}

	// 已知事实：k02 交付后被撤权仍保留在交付日志；k05 跳过后被重新授权也未补发。
	if !contains(delivered, "k02") {
		t.Fatal("k02 missing from delivered log after post-delivery revocation")
	}
	if contains(delivered, "k05") || !skipSet["k05"] {
		t.Fatal("k05 must stay skipped despite re-grant")
	}
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}
