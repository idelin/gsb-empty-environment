package netsec

import "testing"

func ip(i int) *int { return &i }

var hostDisk = Device{Type: TypeBlock, Major: 8, Minor: 0}

func compileOrFail(t *testing.T, rules []Rule) *Diagram {
	t.Helper()
	d, err := Compile(rules)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if err := d.checkInvariant(); err != nil {
		t.Fatalf("diagram invariant: %v", err)
	}
	return d
}

// 先 allow all 后 deny 磁盘：deny 只删 r 时，宿主磁盘仍握有 w。
func TestAllowAllThenDenyDisk(t *testing.T) {
	d := compileOrFail(t, []Rule{
		{Effect: Allow, Type: TypeAll, Perms: PermAll},
		{Effect: Deny, Type: TypeBlock, Major: ip(8), Minor: ip(0), Perms: PermRead},
	})
	if got := d.Query(hostDisk); got != PermWrite|PermMknod {
		t.Fatalf("host disk perms = %v, want wm", got)
	}
	if got := d.Query(Device{Type: TypeChar, Major: 1, Minor: 3}); got != PermAll {
		t.Fatalf("char 1:3 perms = %v, want rwm", got)
	}
	// 完整 deny 后磁盘应无任何权限。
	d2 := compileOrFail(t, []Rule{
		{Effect: Allow, Type: TypeAll, Perms: PermAll},
		{Effect: Deny, Type: TypeBlock, Major: ip(8), Minor: ip(0), Perms: PermAll},
	})
	if got := d2.Query(hostDisk); got != 0 {
		t.Fatalf("host disk perms = %v, want none", got)
	}
}

// deny 后精确 allow：只有被精确放行的设备获得权限。
func TestDenyThenExactAllow(t *testing.T) {
	d := compileOrFail(t, []Rule{
		{Effect: Deny, Type: TypeAll, Perms: PermAll},
		{Effect: Allow, Type: TypeChar, Major: ip(1), Minor: ip(3), Perms: PermRead | PermWrite},
	})
	if got := d.Query(Device{Type: TypeChar, Major: 1, Minor: 3}); got != PermRead|PermWrite {
		t.Fatalf("char 1:3 perms = %v, want rw", got)
	}
	for _, dev := range []Device{
		{TypeChar, 1, 2}, {TypeChar, 1, 4}, {TypeBlock, 8, 0},
	} {
		if got := d.Query(dev); got != 0 {
			t.Fatalf("%v perms = %v, want none", dev, got)
		}
	}
	rep, err := Verify(d, &Policy{
		Allow: []DevicePerm{{Device{TypeChar, 1, 3}, PermRead | PermWrite}},
		Deny:  []DevicePerm{{hostDisk, PermAll}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Sound || !rep.Complete {
		t.Fatalf("report = %+v, want sound and complete", rep)
	}
}

// 类型 a 同时匹配 c 和 b；m 不自动附带 r/w。
func TestTypeAllMatchesBoth(t *testing.T) {
	d := compileOrFail(t, []Rule{
		{Effect: Allow, Type: TypeAll, Perms: PermMknod},
	})
	for _, dev := range []Device{
		{TypeChar, 1, 3}, {TypeBlock, 8, 0},
	} {
		if got := d.Query(dev); got != PermMknod {
			t.Fatalf("%v perms = %v, want m only", dev, got)
		}
	}
	d2 := compileOrFail(t, []Rule{
		{Effect: Allow, Type: TypeAll, Perms: PermAll},
		{Effect: Deny, Type: TypeAll, Perms: PermMknod},
	})
	if got := d2.Query(Device{TypeBlock, 8, 0}); got != PermRead|PermWrite {
		t.Fatalf("after deny m: perms = %v, want rw", got)
	}
}

// 权限子集：allow rw 再 deny w 只剩 r。
func TestPermissionSubset(t *testing.T) {
	d := compileOrFail(t, []Rule{
		{Effect: Allow, Type: TypeChar, Major: ip(1), Minor: ip(3), Perms: PermRead | PermWrite},
		{Effect: Deny, Type: TypeChar, Major: ip(1), Minor: ip(3), Perms: PermWrite},
	})
	if got := d.Query(Device{TypeChar, 1, 3}); got != PermRead {
		t.Fatalf("perms = %v, want r", got)
	}
}

// 边界 4095：最大编号设备可精确匹配，越界值被拒绝。
func TestBoundary4095(t *testing.T) {
	edge := Device{Type: TypeBlock, Major: MaxDeviceNumber, Minor: MaxDeviceNumber}
	d := compileOrFail(t, []Rule{
		{Effect: Allow, Type: TypeBlock, Major: ip(MaxDeviceNumber), Minor: ip(MaxDeviceNumber), Perms: PermWrite},
	})
	if got := d.Query(edge); got != PermWrite {
		t.Fatalf("%v perms = %v, want w", edge, got)
	}
	if got := d.Query(Device{TypeBlock, MaxDeviceNumber, MaxDeviceNumber - 1}); got != 0 {
		t.Fatalf("neighbor perms = %v, want none", got)
	}
	if _, err := Compile([]Rule{{Effect: Allow, Type: TypeBlock, Major: ip(4096), Perms: PermRead}}); err == nil {
		t.Fatal("expected error for major 4096")
	}
	if _, err := Compile([]Rule{{Effect: Allow, Type: TypeBlock, Minor: ip(-1), Perms: PermRead}}); err == nil {
		t.Fatal("expected error for minor -1")
	}
}

// 宿主磁盘 w 权限的最小规则链见证：租户为数据卷 8:1 申请 w，
// 通配 minor 写成 b 8 *，一条规则就把宿主磁盘 8:0 的 w 也放了进去。
func TestHostDiskWriteWitness(t *testing.T) {
	tenantVolume := Device{TypeBlock, 8, 1}
	witness := []Rule{
		{Effect: Allow, Type: TypeBlock, Major: ip(8), Perms: PermWrite},
	}
	d := compileOrFail(t, witness)
	if got := d.Query(hostDisk); got&PermWrite == 0 {
		t.Fatalf("witness chain does not grant host disk w: %v", got)
	}
	t.Logf("最小规则链见证: %q -> 宿主磁盘 %v 获得 w 权限", witness[0].String(), hostDisk)

	rep, err := Verify(d, &Policy{
		Allow: []DevicePerm{{tenantVolume, PermWrite}},
		Deny:  []DevicePerm{{hostDisk, PermAll}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Sound {
		t.Fatal("expected unsound: host disk w must be flagged")
	}
	want := DevicePerm{hostDisk, PermWrite}
	if *rep.Violation != want {
		t.Fatalf("violation = %v, want %v", rep.Violation, want)
	}
	// 完整性：租户数据卷 w 被正确放行，无缺失。
	if !rep.Complete {
		t.Fatalf("unexpected completeness hint: %v", rep.Missing)
	}

	// 完整性提示：目标允许 c 1:3 rw，规则未放行时应给出最小误拒设备。
	rep2, err := Verify(d, &Policy{
		Allow: []DevicePerm{
			{tenantVolume, PermWrite},
			{Device{TypeChar, 1, 3}, PermRead | PermWrite},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	wantMissing := DevicePerm{Device{TypeChar, 1, 3}, PermRead}
	if rep2.Complete || *rep2.Missing != wantMissing {
		t.Fatalf("missing = %v, want %v", rep2.Missing, wantMissing)
	}
}

// 待审优化器：交换 allow/deny 顺序改变语义，必须报出首个差异；
// 合并相邻 allow 不改变语义，应判等价。
func TestReviewOptimizer(t *testing.T) {
	base := []Rule{
		{Effect: Deny, Type: TypeAll, Perms: PermAll},
		{Effect: Allow, Type: TypeChar, Major: ip(1), Minor: ip(3), Perms: PermRead | PermWrite},
	}
	reorder := func(rs []Rule) []Rule { return []Rule{rs[1], rs[0]} }
	diff, err := ReviewOptimizer(base, reorder)
	if err != nil {
		t.Fatal(err)
	}
	if diff == nil {
		t.Fatal("expected semantic diff for reordering optimizer")
	}
	want := SemanticDiff{Device{TypeChar, 1, 3}, PermRead | PermWrite, 0}
	if *diff != want {
		t.Fatalf("diff = %v, want %v", diff, want)
	}

	split := []Rule{
		{Effect: Allow, Type: TypeChar, Major: ip(1), Minor: ip(3), Perms: PermRead},
		{Effect: Allow, Type: TypeChar, Major: ip(1), Minor: ip(3), Perms: PermWrite},
	}
	merge := func(rs []Rule) []Rule {
		return []Rule{{Effect: Allow, Type: TypeChar, Major: ip(1), Minor: ip(3), Perms: PermRead | PermWrite}}
	}
	diff, err = ReviewOptimizer(split, merge)
	if err != nil {
		t.Fatal(err)
	}
	if diff != nil {
		t.Fatalf("expected equivalent diagrams, got diff %v", diff)
	}
}

// 验证器不应因有限允许集之外的设备而漏报：allow all 规则下，
// 最小越权设备是 c 0:0 的 r。
func TestVerifyMinimalViolation(t *testing.T) {
	d := compileOrFail(t, []Rule{
		{Effect: Allow, Type: TypeAll, Perms: PermAll},
		{Effect: Deny, Type: TypeBlock, Major: ip(8), Minor: ip(0), Perms: PermAll},
	})
	rep, err := Verify(d, &Policy{
		Allow: []DevicePerm{{Device{TypeChar, 1, 3}, PermRead | PermWrite}},
		Deny:  []DevicePerm{{hostDisk, PermAll}},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := DevicePerm{Device{TypeChar, 0, 0}, PermRead}
	if rep.Sound || *rep.Violation != want {
		t.Fatalf("violation = %v, want %v", rep.Violation, want)
	}
}
