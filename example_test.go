package quota_test

import (
	"context"
	"fmt"

	"phasequota"
)

// ExampleRun 演示任务依次申请 CPU、IO 阶段额度并在结束后归还。
func Example() {
	sched := quota.New(map[quota.Phase]int{quota.CPU: 1, quota.IO: 1})

	err := sched.Run(context.Background(),
		[]quota.Phase{quota.CPU, quota.IO},
		map[quota.Phase]quota.Work{
			quota.CPU: func(context.Context) error {
				fmt.Println("doing CPU work; free io =", sched.Available(quota.IO))
				return nil
			},
			quota.IO: func(context.Context) error {
				fmt.Println("doing IO work; free cpu =", sched.Available(quota.CPU))
				return nil
			},
		},
	)
	fmt.Println("err:", err)
	fmt.Printf("restored: cpu=%d io=%d\n", sched.Available(quota.CPU), sched.Available(quota.IO))
	// Output:
	// doing CPU work; free io = 1
	// doing IO work; free cpu = 1
	// err: <nil>
	// restored: cpu=1 io=1
}
