package leasearb

import (
	"bufio"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
)

// 事件脚本 DSL（按行解析，# 开头为注释）：
//
//   clock +5s                    推进手动时钟（模拟时钟前跳）
//   acquire <holder> <ttl>       申请租约
//   renew <holder> <gen> <ttl>   续租
//   transfer <from> <to> <ttl>   显式交接
//   write <holder> <gen> <reqID> 提交一次写入仲裁
//   expect-lease <holder> <gen>  断言当前有效租约
//   expect-no-lease              断言当前无有效租约
//   expect-write <reqID> <判定>  断言某次写入的仲裁结果

// Runner 重放事件脚本并输出可读的执行轨迹。
type Runner struct {
	arb     *Arbiter
	clock   *ManualClock
	start   time.Time
	out     io.Writer
	writes  map[string]Verdict // requestID -> 最近一次判定
	lineNum int
	failed  bool
}

// NewRunner 创建重放器：手动时钟从 start 开始，轨迹写入 out。
func NewRunner(start time.Time, out io.Writer) *Runner {
	clk := NewManualClock(start)
	return &Runner{
		arb:    NewArbiter(clk),
		clock:  clk,
		start:  start,
		out:    out,
		writes: make(map[string]Verdict),
	}
}

// Arbiter 返回重放器驱动的仲裁器，便于脚本跑完后继续断言。
func (r *Runner) Arbiter() *Arbiter { return r.arb }

// Run 解析并逐行重放脚本；任何 expect 失败或指令错误都会使返回 error。
func (r *Runner) Run(script string) error {
	sc := bufio.NewScanner(strings.NewReader(script))
	for sc.Scan() {
		r.lineNum++
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			if strings.HasPrefix(line, "#") && len(line) > 1 {
				r.trace("#", "%s", strings.TrimSpace(strings.TrimPrefix(line, "#")))
			}
			continue
		}
		if err := r.step(strings.Fields(line)); err != nil {
			return fmt.Errorf("line %d [%s]: %v", r.lineNum, line, err)
		}
	}
	if err := sc.Err(); err != nil {
		return err
	}
	if r.failed {
		return fmt.Errorf("script finished with failed expectations")
	}
	return nil
}

func (r *Runner) trace(op, format string, args ...interface{}) {
	elapsed := r.clock.Now().Sub(r.start)
	fmt.Fprintf(r.out, "t=%-9s %-28s %s\n",
		elapsed.String(), op, fmt.Sprintf(format, args...))
}

func (r *Runner) fail(format string, args ...interface{}) {
	r.failed = true
	r.trace("EXPECT-FAIL", "✗ "+format, args...)
}

func (r *Runner) step(f []string) error {
	switch f[0] {
	case "clock":
		d, err := time.ParseDuration(strings.TrimPrefix(f[1], "+"))
		if err != nil {
			return err
		}
		r.clock.Advance(d)
		r.trace("clock +"+d.String(), "-> now=%s", r.clock.Now().UTC().Format("15:04:05"))

	case "acquire":
		ttl, err := time.ParseDuration(f[2])
		if err != nil {
			return err
		}
		l, err := r.arb.Acquire(f[1], ttl)
		if err != nil {
			r.trace("acquire "+f[1], "-> error: %v", err)
		} else {
			r.trace("acquire "+f[1], "-> %s", l)
		}

	case "renew":
		gen, err := strconv.ParseUint(f[2], 10, 64)
		if err != nil {
			return err
		}
		ttl, err := time.ParseDuration(f[3])
		if err != nil {
			return err
		}
		l, err := r.arb.Renew(f[1], gen, ttl)
		if err != nil {
			r.trace(fmt.Sprintf("renew %s gen=%d", f[1], gen), "-> error: %v", err)
		} else {
			r.trace(fmt.Sprintf("renew %s gen=%d", f[1], gen), "-> %s", l)
		}

	case "transfer":
		ttl, err := time.ParseDuration(f[3])
		if err != nil {
			return err
		}
		l, err := r.arb.Transfer(f[1], f[2], ttl)
		if err != nil {
			r.trace(fmt.Sprintf("transfer %s->%s", f[1], f[2]), "-> error: %v", err)
		} else {
			r.trace(fmt.Sprintf("transfer %s->%s", f[1], f[2]), "-> %s", l)
		}

	case "write":
		gen, err := strconv.ParseUint(f[2], 10, 64)
		if err != nil {
			return err
		}
		v := r.arb.AdmitWrite(f[1], gen, f[3])
		r.writes[f[3]] = v
		r.trace(fmt.Sprintf("write %s gen=%d %s", f[1], gen, f[3]), "-> %s", v)

	case "expect-lease":
		gen, err := strconv.ParseUint(f[2], 10, 64)
		if err != nil {
			return err
		}
		l, active := r.arb.Snapshot()
		if !active || l.Holder != f[1] || l.Generation != gen {
			r.fail("expect-lease %s gen=%d, got %s active=%v", f[1], gen, l, active)
		} else {
			r.trace(fmt.Sprintf("expect-lease %s gen=%d", f[1], gen), "-> ok (%s)", l)
		}

	case "expect-no-lease":
		l, active := r.arb.Snapshot()
		if active {
			r.fail("expect-no-lease, got %s", l)
		} else {
			r.trace("expect-no-lease", "-> ok (last: %s)", l)
		}

	case "expect-write":
		want, ok := parseVerdict(f[2])
		if !ok {
			return fmt.Errorf("unknown verdict %q", f[2])
		}
		got, seen := r.writes[f[1]]
		if !seen || got != want {
			r.fail("expect-write %s %s, got %v (seen=%v)", f[1], want, got, seen)
		} else {
			r.trace(fmt.Sprintf("expect-write %s %s", f[1], want), "-> ok")
		}

	default:
		return fmt.Errorf("unknown directive %q", f[0])
	}
	return nil
}

func parseVerdict(s string) (Verdict, bool) {
	for v := Accepted; v <= LeaseExpired; v++ {
		if v.String() == s {
			return v, true
		}
	}
	return Accepted, false
}
