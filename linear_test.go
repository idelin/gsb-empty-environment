package netsec

import "testing"

// TestLinearSafeAccess：纯基址+常数的线性读取应得到在界内证书。
func TestLinearSafeAccess(t *testing.T) {
	prog := &Program{
		Regions: []Region{{Name: "mem", Base: 1000, Size: 64}},
		Blocks: []Block{{
			Ops: []Op{
				{Kind: OpBase, Dst: t0, Region: 0},
				{Kind: OpAddConst, Dst: t1, Src: t0, Const: 8},
				// 索引为常量 0：复制基址后其偏移区间即 [0,0]。
				{Kind: OpCopy, Dst: t2, Src: t0},
				ld(t3, t1, t2, 0, 4, "ld0"),
			},
			Term: retTerm(),
		}},
		Entry: 0,
	}

	rep, cross, err := Validate(prog)
	if err != nil {
		t.Fatalf("Validate 返回错误: %v", err)
	}
	if !rep.OK {
		t.Fatalf("期望分析通过，得到违例: %+v", rep.Violations)
	}
	if len(rep.Certificates) != 1 {
		t.Fatalf("期望 1 张证书，得到 %d", len(rep.Certificates))
	}
	cert := rep.Certificates[0]
	if cert.Site != "ld0" || len(cert.Origins) != 1 || cert.Origins[0] != 0 {
		t.Fatalf("证书来源错误: %+v", cert)
	}
	// 基址 1000 + 偏移 8，读 4 字节 -> 绝对闭区间 [1008,1011]。
	if cert.AccessLo != 1008 || cert.AccessHi != 1011 {
		t.Fatalf("访问区间错误: [%d,%d]", cert.AccessLo, cert.AccessHi)
	}
	if !cross.OK || cross.Checked != 1 {
		t.Fatalf("交叉核对失败: %+v", cross)
	}
}

// TestConstantOutOfBounds：常量索引越界必须被拒绝。
func TestConstantOutOfBounds(t *testing.T) {
	prog := &Program{
		Regions: []Region{{Name: "mem", Base: 0, Size: 16}},
		Blocks: []Block{{
			Ops: []Op{
				{Kind: OpBase, Dst: t0, Region: 0},
				{Kind: OpAddConst, Dst: t1, Src: t0, Const: 20},
				{Kind: OpCopy, Dst: t2, Src: t0},
				ld(t3, t1, t2, 0, 1, "ldOob"),
			},
			Term: retTerm(),
		}},
		Entry: 0,
	}
	rep, cross, err := Validate(prog)
	if err != nil {
		t.Fatalf("Validate 错误: %v", err)
	}
	if rep.OK {
		t.Fatalf("常量越界读取必须被拒绝")
	}
	if !cross.OK {
		t.Fatalf("静态正确拒绝时交叉核对不应判不一致: %+v", cross.Mismatches)
	}
	if len(cross.ConfirmedOutOfBounds) != 1 || cross.ConfirmedOutOfBounds[0] != "ldOob" {
		t.Fatalf("应确认具体越界: %+v", cross.ConfirmedOutOfBounds)
	}
}
