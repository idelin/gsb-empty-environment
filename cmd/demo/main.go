// demo 运行一次带丢包的传输仿真并打印事件日志。
package main

import (
	"fmt"

	sim "tcpsim"
)

func main() {
	const mss = 1000
	// 确定性丢包：seq=4000 的数据段第一次经过链路时丢失，
	// 触发重复 ACK（不增窗）与超时回退。
	seen := map[int]int{}
	drop := func(p sim.Packet, tick int) bool {
		seen[p.Seq]++
		return p.Seq == 4000 && seen[p.Seq] == 1
	}

	s := sim.New(sim.Config{
		MSS:       mss,
		DataLen:   16 * mss,
		InitCwnd:  1 * mss,
		Ssthresh:  8 * mss,
		RTO:       40,
		MaxRTO:    320,
		LinkDelay: 5,
		DropData:  drop,
		Verbose:   true,
	})
	s.Run(100000)

	for _, line := range s.Log.Lines {
		fmt.Println(line)
	}
	fmt.Println("---- summary ----")
	fmt.Printf("done=%v delivered=%d/%d bytes ticks=%d\n",
		s.Sender.Done, s.Receiver.Delivered, 16*mss, s.Sched.Now())
	fmt.Printf("timeouts=%d dupAcks(sender)=%d dupAcks(receiver)=%d finalCwnd=%d ssthresh=%d rto=%d\n",
		s.Sender.Timeouts, s.Sender.DupAcks, s.Receiver.DupAcksSent,
		s.Sender.Cwnd, s.Sender.Ssthresh, s.Sender.RTO)
	fmt.Printf("data sent=%d dropped=%d, ack sent=%d dropped=%d\n",
		s.DataLink.Sent, s.DataLink.Dropped, s.AckLink.Sent, s.AckLink.Dropped)
	fmt.Printf("cwnd trace: %v\n", s.Sender.CwndTrace())
}
