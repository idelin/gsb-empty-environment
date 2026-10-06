package merge

import (
	"math/rand"
	"reflect"
	"testing"
	"time"
)

// 随机生成候选集，核对单遍实现与直接排序筛选参照模型结果完全一致。
func TestAdjudicateMatchesNaiveModel(t *testing.T) {
	rng := rand.New(rand.NewSource(20261007))
	fields := []string{"name", "email", "status", "age"}
	sources := []string{"crm", "erp", "web", "auth"}
	base := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)

	for iter := 0; iter < 20000; iter++ {
		n := rng.Intn(9)
		cands := make([]Candidate, n)
		for i := range cands {
			c := Candidate{
				Field:    fields[rng.Intn(len(fields))],
				Source:   sources[rng.Intn(len(sources))],
				Priority: rng.Intn(4),
				Seq:      uint64(rng.Intn(4)),
			}
			// 1/3 概率带有效区间，窗口围绕查询时间随机偏移
			if rng.Intn(3) == 0 {
				from := base.AddDate(0, 0, rng.Intn(11)-5)
				to := base.AddDate(0, 0, rng.Intn(11)-5)
				if to.Before(from) {
					from, to = to, from
				}
				c.From, c.To = from, to
			}
			switch rng.Intn(5) {
			case 0:
				c.Value = MissingValue()
			case 1:
				c.Value = NullValue()
			default:
				c.Value = ValueOf(rng.Intn(4)) // 小值域以制造同值/异值并列
			}
			cands[i] = c
		}
		qt := base.AddDate(0, 0, rng.Intn(7)-3)
		got := Adjudicate(cands, qt)
		want := NaiveAdjudicate(cands, qt)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("第 %d 次迭代不一致\ncands=%#v\nt=%v\ngot =%+v\nwant=%+v", iter, cands, qt, got, want)
		}
	}
}
