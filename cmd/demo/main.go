// demo 演示同主机多进程互斥协议：
// 父进程启动一个真实子进程持锁，随后将其 SIGKILL 模拟崩溃，
// 展示陈旧锁接管、隔离令牌递增与旧令牌拒绝。
//
// 运行：go run ./cmd/demo -dir /tmp/mutexdemo
package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"time"

	"hostmutex/mutex"
)

func main() {
	dir := flag.String("dir", "", "锁文件目录（调用方指定）")
	name := flag.String("name", "demo", "锁名")
	ttl := flag.Duration("ttl", 10*time.Second, "租约时长")
	worker := flag.Bool("worker", false, "以持锁工人模式运行（内部使用）")
	flag.Parse()
	if *dir == "" {
		fmt.Fprintln(os.Stderr, "必须指定 -dir")
		os.Exit(2)
	}
	if *worker {
		runWorker(*dir, *name, *ttl)
		return
	}
	runDemo(*dir, *name, *ttl)
}

// runWorker 获取锁并持续续约，直到被杀死（模拟持锁中的进程）。
func runWorker(dir, name string, ttl time.Duration) {
	h, err := mutex.New(dir, name, &mutex.Options{LeaseTTL: ttl})
	if err != nil {
		fmt.Fprintln(os.Stderr, "worker:", err)
		os.Exit(1)
	}
	tok, err := h.Acquire()
	if err != nil {
		fmt.Fprintln(os.Stderr, "worker acquire:", err)
		os.Exit(1)
	}
	fmt.Printf("TOKEN=%d\n", tok)
	for {
		time.Sleep(ttl / 3)
		if err := h.Renew(); err != nil {
			fmt.Fprintln(os.Stderr, "worker renew:", err)
			os.Exit(1)
		}
	}
}

func runDemo(dir, name string, ttl time.Duration) {
	exe, err := os.Executable()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	step := func(format string, args ...any) { fmt.Printf("== "+format+"\n", args...) }

	step("1. 启动子进程持锁（pid 待分配）")
	cmd := exec.Command(exe, "-worker", "-dir", dir, "-name", name, "-ttl", ttl.String())
	stdout, _ := cmd.StdoutPipe()
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	var childToken uint64
	if _, err := fmt.Fscanf(bufio.NewReader(stdout), "TOKEN=%d\n", &childToken); err != nil {
		fmt.Fprintln(os.Stderr, "read token:", err)
		os.Exit(1)
	}
	fmt.Printf("   子进程 pid=%d 持锁，隔离令牌=%d\n", cmd.Process.Pid, childToken)

	parent, err := mutex.New(dir, name, &mutex.Options{LeaseTTL: ttl})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	step("2. 父进程尝试获取 → 应被拒绝（live）")
	if _, err := parent.Acquire(); err != nil {
		fmt.Printf("   获取失败（符合预期）: %v\n", err)
	}
	rec, st, _ := parent.Inspect()
	fmt.Printf("   状态=%s 持有者 pid=%d host=%s\n", st, rec.PID, rec.Host)

	step("3. SIGKILL 子进程，模拟崩溃")
	cmd.Process.Kill()
	cmd.Wait()
	_, st, _ = parent.Inspect()
	fmt.Printf("   崩溃后状态=%s（stale，可安全接管）\n", st)

	step("4. 父进程接管锁")
	tok, err := parent.Acquire()
	if err != nil {
		fmt.Fprintln(os.Stderr, "takeover:", err)
		os.Exit(1)
	}
	fmt.Printf("   接管成功，新隔离令牌=%d（旧=%d，严格递增）\n", tok, childToken)

	step("5. 崩溃的旧持有者若凭旧令牌提交 → 必须拒绝")
	if err := parent.ValidateToken(childToken); err != nil {
		fmt.Printf("   旧令牌 %d 被拒绝（符合预期）: %v\n", childToken, err)
	}
	if err := parent.ValidateToken(tok); err == nil {
		fmt.Printf("   当前令牌 %d 校验通过\n", tok)
	}

	step("6. 父进程释放锁")
	if err := parent.Release(); err != nil {
		fmt.Fprintln(os.Stderr, "release:", err)
		os.Exit(1)
	}
	_, st, _ = parent.Inspect()
	fmt.Printf("   释放后状态=%s\n", st)
	fmt.Println("演示完成。锁文件位于:", dir)
}
