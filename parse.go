package netsec

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// ParseScript 解析文本脚本，供宿主以文本形式输入各线程指令。
//
// 格式：每行一条指令，以 "T<线程号>" 开头（线程号 0..2），
// 同一线程的指令按出现顺序组成其程序。支持 # 注释与空行。
//
//	T0 read h          # h = head
//	T0 next n h        # n = deref(h).next
//	T1 alloc x 7       # 分配节点，事件 ID 为 7
//	T1 free h          # 释放 h 指向的槽
//	T0 cas h n         # CAS(head, h, n)
//	T1 setnext x h     # deref(x).next = h
func ParseScript(text string) ([][]Op, error) {
	byThread := map[int][]Op{}
	lines := strings.Split(text, "\n")
	for ln, raw := range lines {
		line := raw
		if i := strings.IndexByte(line, '#'); i >= 0 {
			line = line[:i]
		}
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		where := fmt.Sprintf("第 %d 行", ln+1)
		tok := fields[0]
		if len(tok) < 2 || (tok[0] != 'T' && tok[0] != 't') {
			return nil, fmt.Errorf("%s: 缺少线程前缀 T<n>: %q", where, raw)
		}
		tid, err := strconv.Atoi(tok[1:])
		if err != nil || tid < 0 || tid >= MaxThreads {
			return nil, fmt.Errorf("%s: 非法线程号 %q（允许 T0..T%d）", where, tok, MaxThreads-1)
		}
		if len(fields) < 2 {
			return nil, fmt.Errorf("%s: 缺少指令", where)
		}
		args := fields[2:]
		var op Op
		switch strings.ToLower(fields[1]) {
		case "read":
			if len(args) != 1 {
				return nil, fmt.Errorf("%s: read 需要 1 个参数", where)
			}
			op = Read(args[0])
		case "next":
			if len(args) != 2 {
				return nil, fmt.Errorf("%s: next 需要 2 个参数", where)
			}
			op = Next(args[0], args[1])
		case "alloc":
			if len(args) != 2 {
				return nil, fmt.Errorf("%s: alloc 需要寄存器与事件 ID", where)
			}
			ev, err := strconv.Atoi(args[1])
			if err != nil {
				return nil, fmt.Errorf("%s: 非法事件 ID %q", where, args[1])
			}
			op = Alloc(args[0], ev)
		case "free":
			if len(args) != 1 {
				return nil, fmt.Errorf("%s: free 需要 1 个参数", where)
			}
			op = Free(args[0])
		case "cas":
			if len(args) != 2 {
				return nil, fmt.Errorf("%s: cas 需要期望/新值寄存器", where)
			}
			op = CAS(args[0], args[1])
		case "setnext":
			if len(args) != 2 {
				return nil, fmt.Errorf("%s: setnext 需要节点/值寄存器", where)
			}
			op = SetNext(args[0], args[1])
		default:
			return nil, fmt.Errorf("%s: 未知指令 %q", where, fields[1])
		}
		byThread[tid] = append(byThread[tid], op)
	}
	if len(byThread) == 0 {
		return nil, fmt.Errorf("脚本为空")
	}
	tids := make([]int, 0, len(byThread))
	for t := range byThread {
		tids = append(tids, t)
	}
	sort.Ints(tids)
	for i, t := range tids {
		if t != i {
			return nil, fmt.Errorf("线程号必须从 T0 连续编号，缺少 T%d", i)
		}
	}
	scripts := make([][]Op, len(tids))
	for i, t := range tids {
		scripts[i] = byThread[t]
	}
	return scripts, Validate(scripts)
}
