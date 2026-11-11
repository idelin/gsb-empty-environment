package dictneg

import "testing"

// TestFileStoreCrashRecovery 用真实文件系统验证：进程重启（新实例）
// 后能看到全部已持久确认的状态与字典内容。
func TestFileStoreCrashRecovery(t *testing.T) {
	dir := t.TempDir()
	store, err := NewFileStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	dict := []byte("file-backed-dictionary")
	digest := SumDigest(dict)
	if err := store.PutDictionary(digest, dict); err != nil {
		t.Fatal(err)
	}
	ps := &ProposerState{Epoch: 3, Active: digest, ActiveConfirmed: true}
	if err := store.SaveProposerState("s1", ps); err != nil {
		t.Fatal(err)
	}
	as := &AcceptorState{
		Epoch: 3, EnabledEpoch: 3, Active: digest,
		Installed: []Digest{digest},
		NextSeq:   map[string]uint64{digest.String(): 11},
	}
	if err := store.SaveAcceptorState("s1", as); err != nil {
		t.Fatal(err)
	}

	// 模拟重启：全新实例读同一目录。
	reopened, err := NewFileStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	content, err := reopened.GetDictionary(digest)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != string(dict) {
		t.Fatalf("dict = %q", content)
	}
	prop, err := reopened.LoadProposerState("s1")
	if err != nil {
		t.Fatal(err)
	}
	if prop == nil || prop.Active != digest || !prop.ActiveConfirmed || prop.Epoch != 3 {
		t.Fatalf("proposer state = %+v", prop)
	}
	acc, err := reopened.LoadAcceptorState("s1")
	if err != nil {
		t.Fatal(err)
	}
	if acc == nil || acc.Active != digest || acc.NextSeq[digest.String()] != 11 {
		t.Fatalf("acceptor state = %+v", acc)
	}

	// 从未写入的命名空间返回零状态而不是错误。
	empty, err := reopened.LoadProposerState("never-written")
	if err != nil || empty != nil {
		t.Fatalf("missing state = %+v, %v", empty, err)
	}
	if _, err := reopened.GetDictionary(SumDigest([]byte("nope"))); err != ErrUnknownDictionary {
		t.Fatalf("missing dict: %v", err)
	}
}

// TestFileStoreEndToEnd 用 FileStore 跑完整双端协商与重连，
// 验证真实持久化路径上的行为与内存实现一致。
func TestFileStoreEndToEnd(t *testing.T) {
	pStore, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	aStore, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	e := newEndpoints(t, pStore, aStore)
	inst, _ := e.deliverInstall([]byte("dict-on-disk"))
	e.deliverEnable(e.mustEnable())

	e.recv(e.sendFrames(0, 1)...)
	if len(*e.got) != 2 {
		t.Fatalf("delivered %d", len(*e.got))
	}

	// 重启后活动字典与序号进度都要保留。
	e.restart()
	assertActive(t, e, inst.Digest)
	e.recv(e.sendFrames(2)...)
	if len(*e.got) != 3 || (*e.got)[2].seq != 2 {
		t.Fatalf("seq continuity lost after restart: %+v", *e.got)
	}
}
