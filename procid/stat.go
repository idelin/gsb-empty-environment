package procid

import (
	"errors"
	"strconv"
	"strings"
)

const deletedSuffix = " (deleted)"

// StatInfo 是 /proc/PID/stat 中本包关心的字段。
type StatInfo struct {
	// PID 为 stat 第 1 字段，用于和 SO_PEERCRED 给出的 PID 交叉校验。
	PID int
	// Comm 为第 2 字段（圆括号内的可执行名），仅作上下文记录，
	// 任何安全判定都不得依赖它——该字段可被攻击者任意改写。
	Comm string
	// Starttime 为第 22 字段，进程启动时刻（clock ticks since boot）。
	// 同一 PID 上该值变化即意味着原 task 已退出、PID 被新进程复用。
	Starttime uint64
}

// ProcFS 抽象 procfs 读取，生产实现见 LinuxProcFS，测试用内存假实现注入。
type ProcFS interface {
	// ReadStat 返回 pid 对应 /proc/pid/stat 的原始文本。
	ReadStat(pid int) (string, error)
	// ReadExe 返回 readlink("/proc/pid/exe") 的结果；
	// 镜像被替换时内核会追加 " (deleted)" 后缀。
	ReadExe(pid int) (string, error)
}

// ParseStat 解析 /proc/PID/stat。
//
// stat 的第 2 字段 comm 被圆括号包裹，且允许包含空格与 ')'，因此必须
// 以最后一个 ')' 作为 comm 的结束位置，之后的字段再按空白切分。
// 形如：123 (ev)l (x)) S 1 ...，最后一个 ')' 之后才是 state 与后续字段。
func ParseStat(data string) (StatInfo, error) {
	open := strings.IndexByte(data, '(')
	closeIdx := strings.LastIndexByte(data, ')')
	if open <= 0 || closeIdx < open+1 {
		return StatInfo{}, ErrStatUnparseable
	}

	head := strings.TrimSpace(data[:open])
	pid, err := strconv.Atoi(head)
	if err != nil || pid <= 0 {
		return StatInfo{}, ErrStatUnparseable
	}

	comm := data[open+1 : closeIdx]

	// 最后一个 ')' 之后：state 是第 3 字段，starttime 是第 22 字段，
	// 即后缀切片中的第 20 个空白分隔 token（下标 19）。
	rest := strings.Fields(data[closeIdx+1:])
	const starttimeIndex = 19
	if len(rest) <= starttimeIndex {
		return StatInfo{}, ErrStatUnparseable
	}
	start, err := strconv.ParseUint(rest[starttimeIndex], 10, 64)
	if err != nil {
		return StatInfo{}, ErrStatUnparseable
	}

	return StatInfo{PID: pid, Comm: comm, Starttime: start}, nil
}

// IsExeDeleted 判定 readlink("/proc/pid/exe") 的结果是否表示原可执行
// 文件已被 unlink/替换（内核追加 " (deleted)" 后缀）。
func IsExeDeleted(exe string) bool {
	return strings.HasSuffix(exe, deletedSuffix)
}

// normalizeExe 去掉 deleted 后缀，得到真实的镜像路径，用于和绑定时的
// 基线路径比对。
func normalizeExe(exe string) string {
	return strings.TrimSuffix(exe, deletedSuffix)
}

// statFor 读取并解析 procfs stat，同时把 stat 自报的 PID 与请求 PID 比对，
// 防止读到半回收/半重建状态下属于另一个 task 的文件。
func statFor(proc ProcFS, pid int) (StatInfo, error) {
	raw, err := proc.ReadStat(pid)
	if err != nil {
		return StatInfo{}, err
	}
	info, err := ParseStat(raw)
	if err != nil {
		return StatInfo{}, err
	}
	if info.PID != pid {
		return StatInfo{}, errors.Join(ErrPIDReused, errStatPIDMismatch(pid, info.PID))
	}
	return info, nil
}

type pidMismatchError struct{ want, got int }

func (e pidMismatchError) Error() string {
	return "procid: stat pid mismatch: requested " + strconv.Itoa(e.want) +
		", got " + strconv.Itoa(e.got)
}

func errStatPIDMismatch(want, got int) error { return pidMismatchError{want: want, got: got} }
