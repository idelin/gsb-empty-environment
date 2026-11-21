package commit_test

import "prescreen.local/ps-3378167e8b/commit"

func ptr(id commit.SectorID) *commit.SectorID { return &id }

func mkSector(size int, fill byte) []byte {
	b := make([]byte, size)
	for i := range b {
		b[i] = fill
	}
	return b
}

func absCyl(x int64) int64 {
	if x < 0 {
		return -x
	}
	return x
}

// distanceCost：同柱面零费用，否则按柱面距离收费。
func distanceCost(head, cyl commit.Cylinder) int64 {
	return absCyl(int64(head - cyl))
}

func cloneDisk(in map[commit.SectorID][]byte) map[commit.SectorID][]byte {
	out := make(map[commit.SectorID][]byte, len(in))
	for k, v := range in {
		cp := make([]byte, len(v))
		copy(cp, v)
		out[k] = cp
	}
	return out
}

// assertSpyMatchesPlan 核对实际底盘访问与计划动作逐一对应，没有任何计划外偷看。
func assertSpyMatchesPlan(t interface {
	Helper()
	Fatalf(string, ...any)
},
	spy *commit.SpyDevice, plan *commit.Plan) {
	t.Helper()
	var want []commit.SpyCall
	for _, op := range plan.Ops {
		if op.Kind == "read" || op.Kind == "write" {
			want = append(want, commit.SpyCall{Kind: op.Kind, Sector: op.Sector})
		}
	}
	if len(spy.Calls) != len(want) {
		t.Fatalf("device calls = %v, want %v", spy.Calls, want)
	}
	for i := range want {
		if spy.Calls[i] != want[i] {
			t.Fatalf("call %d = %+v, want %+v", i, spy.Calls[i], want[i])
		}
	}
}

// assertFinalDisk 用入口盘快照独立计算期望负载并逐扇区核对最终完整盘。
func assertFinalDisk(t interface {
	Helper()
	Fatalf(string, ...any)
},
	inst *commit.Instance, entry, final map[commit.SectorID][]byte) {
	t.Helper()
	want, err := commit.ExpectedPayloads(inst, entry)
	if err != nil {
		t.Fatalf("expected payloads: %v", err)
	}
	for addr, payload := range want {
		if string(final[addr]) != string(payload) {
			t.Fatalf("sector %d final = %v, want %v", addr, final[addr], payload)
		}
	}
	if len(final) != len(entry) {
		t.Fatalf("disk sector set changed: %d vs %d", len(final), len(entry))
	}
}
