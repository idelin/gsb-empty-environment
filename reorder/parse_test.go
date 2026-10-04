package reorder

import "testing"

func TestParseRoundTrip(t *testing.T) {
	src := `
# 注释
r1 = li 42
r2 = mov r1
r3 = add r1, r2
r4 = sub r3, r1
r5 = xor r1, r2
r6 = and r1, r2
r7 = or r1, r2
r1 = load [0, 4)
r2 = load [8]
[0, 4) = store r1
[8] = store r2
barrier
r9 = load [0x10, 0x14)
`
	insts, err := Parse(src)
	if err != nil {
		t.Fatalf("Parse 失败: %v", err)
	}
	if len(insts) != 13 {
		t.Fatalf("指令数 = %d，期望 13", len(insts))
	}
	// 规范文本再解析应得到相同指令
	var canon string
	for _, in := range insts {
		canon += in.String() + "\n"
	}
	insts2, err := Parse(canon)
	if err != nil {
		t.Fatalf("规范文本再解析失败: %v", err)
	}
	if len(insts2) != len(insts) {
		t.Fatalf("再解析指令数 = %d，期望 %d", len(insts2), len(insts))
	}
	for i := range insts {
		if insts[i].String() != insts2[i].String() {
			t.Errorf("第 %d 条不一致：%q vs %q", i, insts[i], insts2[i])
		}
	}
	// 单字节与十六进制区间
	if insts[8].Kind != OpLoad || insts[8].Addr != 8 || insts[8].Size != 1 {
		t.Errorf("[8] 解析错误: %+v", insts[8])
	}
	if insts[12].Addr != 0x10 || insts[12].Size != 4 {
		t.Errorf("十六进制区间解析错误: %+v", insts[12])
	}
}

func TestParseErrors(t *testing.T) {
	bad := []string{
		"r1 = foo r2",
		"r1 = mov",
		"1r = li 3",
		"r1 = li xyz",
		"[4, 2) = store r1",
		"[4, 4] = store r1",
		"r1 = load [0, 4]",
		"store = mov r1",
	}
	for _, s := range bad {
		if _, err := Parse(s); err == nil {
			t.Errorf("应报错但未报错: %q", s)
		}
	}
}

func TestReadsWrites(t *testing.T) {
	insts, err := Parse("r1 = load [0, 4)\n[4, 8) = store r1\n")
	if err != nil {
		t.Fatal(err)
	}
	if got := insts[0].Reads()[0]; got != Mem(0, 4) {
		t.Errorf("load 读取位置 = %v", got)
	}
	if got := insts[0].Writes()[0]; got != Reg("r1") {
		t.Errorf("load 写入位置 = %v", got)
	}
	if got := insts[1].Writes()[0]; got != Mem(4, 4) {
		t.Errorf("store 写入位置 = %v", got)
	}
}
