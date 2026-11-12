package procid

import "fmt"

// fakeProcEntry 是假 procfs 中单个 PID 的当前状态，测试可直接改写，
// 用来模拟进程退出、PID 复用、exe 被替换等内核侧变化。
type fakeProcEntry struct {
	stat    string
	exe     string
	statErr error
	exeErr  error
}

// fakeProcFS 是 ProcFS 的内存实现。
type fakeProcFS struct {
	entries map[int]*fakeProcEntry
}

func newFakeProcFS() *fakeProcFS {
	return &fakeProcFS{entries: make(map[int]*fakeProcEntry)}
}

func (f *fakeProcFS) put(pid int, e *fakeProcEntry) { f.entries[pid] = e }

func (f *fakeProcFS) ReadStat(pid int) (string, error) {
	e, ok := f.entries[pid]
	if !ok {
		return "", fmt.Errorf("fake procfs: pid %d: no such process", pid)
	}
	if e.statErr != nil {
		return "", e.statErr
	}
	return e.stat, nil
}

func (f *fakeProcFS) ReadExe(pid int) (string, error) {
	e, ok := f.entries[pid]
	if !ok {
		return "", fmt.Errorf("fake procfs: pid %d: no such process", pid)
	}
	if e.exeErr != nil {
		return "", e.exeErr
	}
	return e.exe, nil
}

// fakePidFD 是 PidFD 的内存实现。alive 只能显式翻转，模拟真实 pidfd
// “句柄不随 PID 复用而漂移”的语义。
type fakePidFD struct {
	pid      int
	alive    bool
	aliveErr error
	closed   bool
}

func (f *fakePidFD) Alive() (bool, error) {
	if f.aliveErr != nil {
		return false, f.aliveErr
	}
	return f.alive, nil
}

func (f *fakePidFD) PID() int { return f.pid }

func (f *fakePidFD) Close() error {
	f.closed = true
	return nil
}

type fakeOpener struct {
	fds   map[int]*fakePidFD
	err   error
	calls int
}

func newFakeOpener() *fakeOpener {
	return &fakeOpener{fds: make(map[int]*fakePidFD)}
}

func (o *fakeOpener) register(fd *fakePidFD) *fakePidFD {
	o.fds[fd.pid] = fd
	return fd
}

func (o *fakeOpener) OpenPidFD(pid int) (PidFD, error) {
	o.calls++
	if o.err != nil {
		return nil, o.err
	}
	fd, ok := o.fds[pid]
	if !ok {
		return nil, fmt.Errorf("fake opener: pid %d not registered", pid)
	}
	return fd, nil
}

// statLine 按真实 /proc/<pid>/stat 的字段顺序构造一行，comm 可以任意
// 畸形（空格、括号），starttime 是第 22 字段，即最后一个 ')' 后的
// 第 20 个 token。
func statLine(pid int, comm string, starttime uint64) string {
	afterParen := []string{
		"S",                   // 3  state
		"1",                   // 4  ppid
		"100",                 // 5  pgrp
		"100",                 // 6  session
		"0",                   // 7  tty_nr
		"-1",                  // 8  tpgid
		"0",                   // 9  flags
		"0",                   // 10 minflt
		"0",                   // 11 cminflt
		"0",                   // 12 majflt
		"0",                   // 13 cmajflt
		"0",                   // 14 utime
		"0",                   // 15 stime
		"0",                   // 16 cutime
		"0",                   // 17 cstime
		"20",                  // 18 priority
		"0",                   // 19 nice
		"1",                   // 20 num_threads
		"0",                   // 21 itrealvalue
		fmt.Sprint(starttime), // 22 starttime
		"1000",                // 23 vsize
		"0",                   // 24 rss
	}
	line := fmt.Sprintf("%d (%s)", pid, comm)
	for _, f := range afterParen {
		line += " " + f
	}
	return line
}
