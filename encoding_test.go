package gsb

import (
	"bytes"
	"testing"
)

func TestEncodeRoundTrip(t *testing.T) {
	r := NewRepo(0)
	data := det(1000)
	if err := r.Insert("main", 0, data); err != nil {
		t.Fatal(err)
	}
	if err := r.Snapshot("snap", "main"); err != nil {
		t.Fatal(err)
	}
	if err := r.Overwrite("main", 100, det(50)); err != nil {
		t.Fatal(err)
	}
	if err := r.Move("main", 0, 500, 100); err != nil {
		t.Fatal(err)
	}
	if err := r.Delete("main", 900, 100); err != nil {
		t.Fatal(err)
	}
	r.Store().Collect()

	liveBefore := r.Store().LiveBytes()
	enc := r.Encode()
	r2, err := Decode(enc)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if got := r2.Store().LiveBytes(); got != liveBefore {
		t.Fatalf("解码后存活字节 = %d，期望 %d", got, liveBefore)
	}
	for _, name := range r.Branches() {
		want, err := r.ReadAll(name)
		if err != nil {
			t.Fatal(err)
		}
		got, err := r2.ReadAll(name)
		if err != nil {
			t.Fatalf("解码后读取分支 %q: %v", name, err)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("分支 %q 往返后内容不符", name)
		}
	}
	if err := r2.VerifyLedger(); err != nil {
		t.Fatal(err)
	}
	// 编码确定性：同状态再编码应逐字节一致。
	if enc2 := r2.Encode(); !bytes.Equal(enc, enc2) {
		t.Fatal("编码不具备确定性")
	}
}

func TestEncodedLayout(t *testing.T) {
	r := NewRepo(0)
	if err := r.Insert("main", 0, det(200)); err != nil {
		t.Fatal(err)
	}
	enc := r.Encode()
	if string(enc[:4]) != storeMagic {
		t.Fatalf("魔数 = %q", enc[:4])
	}
	// 手工核算布局字节数：
	// 仓库头 24 + 每块 (16 + 负载) + 分支区头 4 + 分支记录 (2+4+8+4) + 每跨度 16
	blocks := 4 // ceil(200/64)
	spans := 4
	want := repoHeaderLen + blocks*blockRecHead + 200 + 4 + (2 + len("main") + 8 + 4) + spans*spanRecLen
	if len(enc) != want {
		t.Fatalf("编码长度 = %d，按布局核算期望 %d", len(enc), want)
	}
	if got := r.EncodedSize(); got != want {
		t.Fatalf("EncodedSize = %d，期望 %d", got, want)
	}
}

func TestEncodedDedupLinear(t *testing.T) {
	// 逻辑序列重复同一内容：编码体积只随跨度记录线性增长，物理块记录不变。
	r := NewRepo(0)
	chunk := det(BlockSize)
	if err := r.Insert("main", 0, chunk); err != nil {
		t.Fatal(err)
	}
	size1 := r.EncodedSize()
	for i := 0; i < 31; i++ {
		n, _ := r.Len("main")
		if err := r.Insert("main", n, chunk); err != nil {
			t.Fatal(err)
		}
	}
	size2 := r.EncodedSize()
	logical, _ := r.Len("main")
	if logical != 32*BlockSize {
		t.Fatalf("逻辑长度 = %d", logical)
	}
	// 31 次重复只增加 31 条跨度记录，物理块仍只有 1 条记录。
	if delta := size2 - size1; delta != 31*spanRecLen {
		t.Fatalf("重复内容编码增量 = %d，期望 %d（纯跨度记录）", delta, 31*spanRecLen)
	}
	if size2 >= logical {
		t.Fatalf("编码体积 %d 未小于逻辑体积 %d，去重未生效", size2, logical)
	}
}

func TestDecodeCorrupt(t *testing.T) {
	r := buildRepo(t, det(100))
	enc := r.Encode()
	if _, err := Decode(enc[:len(enc)-3]); err == nil {
		t.Fatal("截断编码应报错")
	}
	bad := append([]byte(nil), enc...)
	bad[0] = 'X'
	if _, err := Decode(bad); err == nil {
		t.Fatal("魔数错误应报错")
	}
}
