// 演示：预留 -> 失败留洞 -> 崩溃恢复 -> 显式跳过封闭空洞 -> 发布推进。
package main

import (
	"fmt"
	"log"
	"os"

	"logalloc"
)

func main() {
	var dir string
	if len(os.Args) > 1 {
		dir = os.Args[1]
	}
	if dir == "" {
		log.Fatal("用法: logalloc-demo <目录>")
	}

	// 第一次运行：预留 4 个批次，3 号预留后"崩溃"前未决定。
	l, _, err := logalloc.Open(dir)
	if err != nil {
		log.Fatal(err)
	}
	a := l.Allocator()
	s1, _ := a.Reserve()
	s2, _ := a.Reserve()
	s3, _ := a.Reserve()
	s4, _ := a.Reserve()
	a.Commit(s1, []byte("batch-1"))
	a.Commit(s2, []byte("batch-2"))
	a.Skip(s4) // 4 号显式跳过
	// s3 预留后写数据"失败"，未 commit 也未 skip -> 空洞
	l.Close()
	fmt.Printf("第一次运行: 预留 1-4, 提交 1/2, 跳过 4, 3 号留洞 (s3=%d)\n", s3)

	// 模拟崩溃后恢复
	l2, rep, err := logalloc.Open(dir)
	if err != nil {
		log.Fatal(err)
	}
	defer l2.Close()
	fmt.Printf("恢复报告: 未决定=%v 已跳过=%v 待发布=%v 下一序号=%d 切点=%d\n",
		rep.Undecided, rep.Skipped, rep.Committed, rep.NextSeq, rep.Checkpoint)

	p := l2.Publisher()
	for {
		out, err := p.Next()
		if err == logalloc.ErrHoleNotClosed {
			fmt.Printf("发布流被空洞挡住 (切点=%d)，显式跳过封闭\n", p.Checkpoint())
			if err := l2.Allocator().Skip(rep.Undecided[0]); err != nil {
				log.Fatal(err)
			}
			continue
		}
		if err == logalloc.ErrNothingToPublish {
			break
		}
		if err != nil {
			log.Fatal(err)
		}
		if out.Skipped {
			fmt.Printf("发布 #%d: <跳过>\n", out.Seq)
		} else {
			fmt.Printf("发布 #%d: %s\n", out.Seq, out.Data)
		}
	}
	if err := l2.Verify(); err != nil {
		log.Fatal(err)
	}
	fmt.Println("核对通过: 连续消费、不重复分配")
}
