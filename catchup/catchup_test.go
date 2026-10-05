package catchup

import (
	"reflect"
	"testing"
)

func mustInstall(t *testing.T, f *Follower) {
	t.Helper()
	if err := f.Install(); err != nil {
		t.Fatalf("Install: %v", err)
	}
}

// 快照换代：gen1 传输途中 Leader 生成 gen2，
// Follower 丢弃 gen1 暂存块，最终只安装 gen2；迟到的 gen1 块被忽略。
func TestSnapshotGenerationChange(t *testing.T) {
	leader := NewLeader(4)
	for _, cmd := range []string{"a", "b", "c", "d", "e"} {
		leader.Append(1, cmd)
	}
	gen1 := leader.Snapshot()

	f := NewFollower(NewMemStore())
	if _, err := f.ReceiveChunk(gen1[0]); err != nil {
		t.Fatalf("gen1 chunk0: %v", err)
	}
	if got := f.StagedReceived(); got != 1 {
		t.Fatalf("staged received = %d, want 1", got)
	}

	// 传输期间日志继续追加，Leader 重新快照产生新代次。
	leader.Append(1, "f")
	leader.Append(1, "g")
	gen2 := leader.Snapshot()
	if gen2[0].Meta.Generation != gen1[0].Meta.Generation+1 {
		t.Fatalf("gen2 generation = %d, want %d", gen2[0].Meta.Generation, gen1[0].Meta.Generation+1)
	}

	complete := false
	for _, c := range gen2 {
		done, err := f.ReceiveChunk(c)
		if err != nil {
			t.Fatalf("gen2 chunk %d: %v", c.Index, err)
		}
		complete = done
	}
	if !complete {
		t.Fatal("gen2 not complete after all chunks")
	}
	mustInstall(t, f)

	if got := f.InstalledGen(); got != gen2[0].Meta.Generation {
		t.Fatalf("installed gen = %d, want %d", got, gen2[0].Meta.Generation)
	}
	if got := f.LastIncludedIndex(); got != 7 {
		t.Fatalf("last included index = %d, want 7", got)
	}
	want := []string{"a", "b", "c", "d", "e", "f", "g"}
	if got := f.State(); !reflect.DeepEqual(got, want) {
		t.Fatalf("state = %v, want %v", got, want)
	}

	// 迟到的旧代次块不得破坏已安装状态。
	if _, err := f.ReceiveChunk(gen1[1]); err != nil {
		t.Fatalf("stale gen1 chunk: %v", err)
	}
	if got := f.State(); !reflect.DeepEqual(got, want) {
		t.Fatalf("state after stale chunk = %v, want %v", got, want)
	}
}

// 最后一块重复：重复投递幂等，状态只安装一次、不重复应用。
func TestDuplicateLastChunk(t *testing.T) {
	leader := NewLeader(4)
	for _, cmd := range []string{"x", "y", "z"} {
		leader.Append(1, cmd)
	}
	chunks := leader.Snapshot()

	f := NewFollower(NewMemStore())
	for _, c := range chunks {
		if _, err := f.ReceiveChunk(c); err != nil {
			t.Fatalf("chunk %d: %v", c.Index, err)
		}
	}
	last := chunks[len(chunks)-1]
	for i := 0; i < 3; i++ {
		complete, err := f.ReceiveChunk(last)
		if err != nil {
			t.Fatalf("duplicate last chunk: %v", err)
		}
		if !complete {
			t.Fatal("duplicate last chunk should still report complete")
		}
	}
	mustInstall(t, f)

	want := []string{"x", "y", "z"}
	if got := f.State(); !reflect.DeepEqual(got, want) {
		t.Fatalf("state = %v, want %v", got, want)
	}
	if got := f.InstalledGen(); got != 1 {
		t.Fatalf("installed gen = %d, want 1", got)
	}

	// 安装后再收到同代次块也应被忽略。
	if _, err := f.ReceiveChunk(last); err != nil {
		t.Fatalf("post-install duplicate: %v", err)
	}
	if got := f.State(); !reflect.DeepEqual(got, want) {
		t.Fatalf("state after post-install duplicate = %v, want %v", got, want)
	}
}

// 安装中断后恢复：全部块落盘暂存后“崩溃”，
// 重启后从暂存区完成安装，且只重放快照索引之后的日志。
func TestInstallInterruptedRecovery(t *testing.T) {
	leader := NewLeader(4)
	for _, cmd := range []string{"m1", "m2", "m3", "m4"} {
		leader.Append(1, cmd)
	}
	chunks := leader.Snapshot()
	snapIndex := chunks[0].Meta.LastIncludedIndex

	store := NewMemStore()
	f := NewFollower(store)
	for _, c := range chunks {
		if _, err := f.ReceiveChunk(c); err != nil {
			t.Fatalf("chunk %d: %v", c.Index, err)
		}
	}
	// 崩溃：不调用 Install，直接丢弃 Follower（暂存区已落盘）。

	// 传输期间 Leader 继续追加日志。
	leader.Append(2, "m5")
	leader.Append(2, "m6")

	// 重启：新 Follower 复用同一持久化暂存区。
	f2 := NewFollower(store)
	if err := f2.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	if got := f2.InstalledGen(); got != 1 {
		t.Fatalf("installed gen after recover = %d, want 1", got)
	}
	if got := f2.LastIncludedIndex(); got != snapIndex {
		t.Fatalf("last included index = %d, want %d", got, snapIndex)
	}
	wantState := []string{"m1", "m2", "m3", "m4"}
	if got := f2.State(); !reflect.DeepEqual(got, wantState) {
		t.Fatalf("state after recover = %v, want %v", got, wantState)
	}

	// 只重放快照索引之后的日志。
	f2.Replay(leader.Entries(f2.LastIncludedIndex()))
	want := []string{"m1", "m2", "m3", "m4", "m5", "m6"}
	if got := f2.State(); !reflect.DeepEqual(got, want) {
		t.Fatalf("state after replay = %v, want %v", got, want)
	}
	if got := len(f2.LogEntries()); got != 2 {
		t.Fatalf("log entries after replay = %d, want 2", got)
	}

	// 重复重放幂等。
	f2.Replay(leader.Entries(0))
	if got := f2.State(); !reflect.DeepEqual(got, want) {
		t.Fatalf("state after re-replay = %v, want %v", got, want)
	}
}

// 部分暂存时崩溃：恢复后保留进度，只需补传剩余块。
func TestRecoverPartialStaging(t *testing.T) {
	leader := NewLeader(4)
	for _, cmd := range []string{"p", "q", "r", "s"} {
		leader.Append(1, cmd)
	}
	chunks := leader.Snapshot()
	if len(chunks) < 2 {
		t.Fatalf("need multiple chunks, got %d", len(chunks))
	}

	store := NewMemStore()
	f := NewFollower(store)
	if _, err := f.ReceiveChunk(chunks[0]); err != nil {
		t.Fatalf("chunk0: %v", err)
	}
	// 崩溃：只暂存了第一块。

	f2 := NewFollower(store)
	if err := f2.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	if got := f2.InstalledGen(); got != 0 {
		t.Fatalf("installed gen = %d, want 0 (not installed)", got)
	}
	if got := f2.StagedReceived(); got != 1 {
		t.Fatalf("staged received after recover = %d, want 1", got)
	}
	complete := false
	for _, c := range chunks[1:] {
		done, err := f2.ReceiveChunk(c)
		if err != nil {
			t.Fatalf("chunk %d: %v", c.Index, err)
		}
		complete = done
	}
	if !complete {
		t.Fatal("not complete after remaining chunks")
	}
	mustInstall(t, f2)
	want := []string{"p", "q", "r", "s"}
	if got := f2.State(); !reflect.DeepEqual(got, want) {
		t.Fatalf("state = %v, want %v", got, want)
	}
}

// 校验和：损坏的块被拒绝且不污染暂存区；未收齐时拒绝安装。
func TestChecksumAndInstallBoundary(t *testing.T) {
	leader := NewLeader(4)
	for _, cmd := range []string{"u", "v", "w"} {
		leader.Append(1, cmd)
	}
	chunks := leader.Snapshot()

	f := NewFollower(NewMemStore())

	// 未收齐时安装必须失败，状态不被替换。
	if err := f.Install(); err == nil {
		t.Fatal("Install should fail with empty staging")
	}

	bad := chunks[0]
	bad.Data = append([]byte(nil), bad.Data...)
	if len(bad.Data) > 0 {
		bad.Data[0] ^= 0xff
	}
	if _, err := f.ReceiveChunk(bad); err != ErrChecksumMismatch {
		t.Fatalf("corrupt chunk err = %v, want ErrChecksumMismatch", err)
	}
	if got := f.StagedReceived(); got != 0 {
		t.Fatalf("staged received = %d, want 0 after corrupt chunk", got)
	}

	// 重传正确块后正常完成。
	complete := false
	for _, c := range chunks {
		done, err := f.ReceiveChunk(c)
		if err != nil {
			t.Fatalf("chunk %d: %v", c.Index, err)
		}
		complete = done
	}
	if !complete {
		t.Fatal("not complete")
	}
	mustInstall(t, f)
	want := []string{"u", "v", "w"}
	if got := f.State(); !reflect.DeepEqual(got, want) {
		t.Fatalf("state = %v, want %v", got, want)
	}
}
