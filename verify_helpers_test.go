package netsec

import "testing"

// newTestTrees 构造两棵两层泛化树：具体值 -> ANY，
// 便于在 level 0（具体）与 level 1（全合并）之间切换。
func newTestTrees(t *testing.T) []*Tree {
	t.Helper()
	region, err := NewTree("region", [][2]string{
		{"北京", "R*"}, {"上海", "R*"},
	})
	if err != nil {
		t.Fatalf("构建 region 树失败: %v", err)
	}
	job, err := NewTree("job", [][2]string{
		{"工程师", "J*"}, {"教师", "J*"},
	})
	if err != nil {
		t.Fatalf("构建 job 树失败: %v", err)
	}
	return []*Tree{region, job}
}

func rec(id, region, job, sensitive string) Record {
	return Record{ID: id, QuasiValues: []string{region, job}, Sensitive: sensitive}
}
