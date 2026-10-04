package reorder

import (
	"io/ioutil"
	"testing"
)

func loadExample(t *testing.T, name string) []Inst {
	t.Helper()
	data, err := ioutil.ReadFile("../examples/" + name)
	if err != nil {
		t.Fatalf("读取示例 %s 失败: %v", name, err)
	}
	insts, err := Parse(string(data))
	if err != nil {
		t.Fatalf("解析示例 %s 失败: %v", name, err)
	}
	return insts
}

func TestExamples(t *testing.T) {
	orig := loadExample(t, "original.txt")

	legal := loadExample(t, "legal.txt")
	if res := Check(orig, legal); !res.Legal() {
		t.Errorf("legal.txt 应判合法，违反边: %v", res.Violated)
	}

	illegal := loadExample(t, "illegal.txt")
	res := Check(orig, illegal)
	if res.Legal() {
		t.Error("illegal.txt 应判非法")
	}
	e, _ := res.Earliest()
	if e.From != 0 || e.To != 2 || e.Kind != DepRAW {
		t.Errorf("illegal.txt 最早冲突边 = %s，期望 #1->#3 RAW", e)
	}
	ex := ExhaustiveCheck(orig, illegal, []uint64{0, 1}, 1<<20)
	if ex.Divergence == nil {
		t.Error("illegal.txt 穷举应发现发散")
	}

	cross := loadExample(t, "illegal_barrier.txt")
	res = Check(orig, cross)
	if res.Legal() {
		t.Error("illegal_barrier.txt 应判非法")
	}
	e, _ = res.Earliest()
	if e.Kind != DepBarrier {
		t.Errorf("illegal_barrier.txt 最早冲突边类型 = %v，期望屏障", e.Kind)
	}
}
