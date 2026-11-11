package netsec

import (
	"fmt"
)

func Example() {
	region, _ := NewTree("region", [][2]string{
		{"海淀", "北京"}, {"朝阳", "北京"}, {"北京", "*"},
	})
	job, _ := NewTree("job", [][2]string{
		{"工程师", "*"}, {"教师", "*"},
	})
	limit, _ := NewThreshold(1, 2)

	v, err := New(Config{
		QuasiIDs:         []string{"region", "job"},
		Trees:            []*Tree{region, job},
		Levels:           []int{1, 1},
		L:                2,
		MinGroupSize:     2,
		MaxRatio:         limit,
		RatioDenominator: RatioOverAll,
	})
	if err != nil {
		panic(err)
	}

	records := []Record{
		{ID: "r1", QuasiValues: []string{"海淀", "工程师"}, Sensitive: "流感"},
		{ID: "r2", QuasiValues: []string{"朝阳", "教师"}, Sensitive: "流感"},
		{ID: "r3", QuasiValues: []string{"海淀", "教师"}, Sensitive: "糖尿病"},
		{ID: "r4", QuasiValues: []string{"朝阳", "工程师"}, Sensitive: UnknownSensitive},
	}
	if err := v.Replace(records); err != nil {
		panic(err)
	}

	report, err := v.RegroupAndVerify()
	if err != nil {
		panic(err)
	}
	fmt.Printf("compliant=%v groups=%d\n", report.Compliant(), len(report.Groups))

	// 撤回 r3 后组内只剩 1 种已知敏感值，未知记录 r4 不能凑成第二类。
	update, err := v.Withdraw("r3")
	if err != nil {
		panic(err)
	}
	min := update.After.MinimalViolation()
	fmt.Printf("after-withdraw compliant=%v reason=%s distinct=%d\n",
		update.After.Compliant(), min.Reasons, min.Diversity.DistinctKnown)

	// 占比证据始终是精确有理数，避免浮点边界问题。
	share := ExactShare(2, 5)
	fmt.Printf("share=%s within-limit=%v\n",
		share.RatString(), share.Cmp(limit) <= 0)

	// Output:
	// compliant=true groups=1
	// after-withdraw compliant=false reason=diversity+ratio distinct=1
	// share=2/5 within-limit=true
}
