package procid

// Binder 在连接建立时采集调用者进程的基线身份。
// Proc 与 Opener 必须由调用方注入（生产环境用 DefaultProcFS /
// DefaultPidOpener，测试用内存假实现）；Binder 本身无状态，可并发复用。
type Binder struct {
	Proc   ProcFS
	Opener PidOpener
}

// Binding 是连接与“连接建立瞬间那个 task”之间的绑定，生命周期跟随连接。
// 认证中间件在每次敏感请求到来时调用 Verify 重新核验，而不是只相信
// Accept 时缓存的结论。
type Binding struct {
	binder *Binder
	pidfd  PidFD

	pid       int
	uid, gid  uint32
	starttime uint64
	exe       string
	comm      string

	closed bool
}

// Bind 用 SO_PEERCRED 得到的对端凭据建立身份绑定。
// 调用顺序上，PeerCred 必须在 Accept 后立即获取，避免 PID 复用时窗。
func (b *Binder) Bind(cred PeerCred) (*Binding, error) {
	if b == nil || b.Proc == nil || b.Opener == nil {
		return nil, ErrNotConfigured
	}
	if cred.PID <= 0 {
		return nil, ErrInvalidPID
	}

	// 先拿 pidfd：它在内核里钉住 task，之后原进程退出也不会让句柄
	// 指向复用了该 PID 的新进程。
	fd, err := b.Opener.OpenPidFD(cred.PID)
	if err != nil {
		return nil, err
	}

	binding := &Binding{
		binder: b,
		pidfd:  fd,
		pid:    cred.PID,
		uid:    cred.UID,
		gid:    cred.GID,
	}

	// 采集基线：starttime 与 exe 都在连接建立时刻落档，后续只做比对。
	info, err := statFor(b.Proc, cred.PID)
	if err != nil {
		_ = fd.Close()
		return nil, err
	}
	exe, err := b.Proc.ReadExe(cred.PID)
	if err != nil {
		_ = fd.Close()
		return nil, err
	}
	if IsExeDeleted(exe) {
		_ = fd.Close()
		return nil, ErrExeDeleted
	}

	binding.starttime = info.Starttime
	binding.comm = info.Comm
	binding.exe = normalizeExe(exe)
	return binding, nil
}

// Verify 在认证时刻重新读取 procfs 并与连接时基线逐一比对。
// 任一不一致都返回策略错误，调用方必须按“拒绝”处理。
func (b *Binding) Verify() error {
	switch {
	case b == nil || b.closed:
		return ErrBindingClosed
	case b.binder == nil || b.binder.Proc == nil:
		return ErrNotConfigured
	}

	// 1) pidfd 确认原来的 task 仍然存活。原进程一旦退出，即使 PID
	//    立刻被复用，这里也会先返回 ESRCH/死亡，杜绝冒名顶替。
	alive, err := b.pidfd.Alive()
	if err != nil {
		return err
	}
	if !alive {
		return ErrProcessExited
	}

	// 2) starttime 必须与基线一致。PID 复用时新 task 的 starttime 不同；
	//    从最后一个 ')' 之后解析，comm 里的空格和 ')' 无法干扰字段定位。
	info, err := statFor(b.binder.Proc, b.pid)
	if err != nil {
		return err
	}
	if info.Starttime != b.starttime {
		return ErrPIDReused
	}

	// 3) exe 不得处于 deleted 状态，归一化后的路径也必须与基线相同。
	exe, err := b.binder.Proc.ReadExe(b.pid)
	if err != nil {
		return err
	}
	if IsExeDeleted(exe) {
		return ErrExeDeleted
	}
	if normalizeExe(exe) != b.exe {
		return ErrExeChanged
	}

	return nil
}

// Close 释放 pidfd；关闭后 Verify 返回 ErrBindingClosed。
func (b *Binding) Close() error {
	if b == nil || b.closed {
		return nil
	}
	b.closed = true
	return b.pidfd.Close()
}

// PID 返回对端连接的 PID（仅作日志用途，安全判定不得只依赖它）。
func (b *Binding) PID() int { return b.pid }

// UID 返回 SO_PEERCRED 记录的对端有效用户 ID。
func (b *Binding) UID() uint32 { return b.uid }

// GID 返回 SO_PEERCRED 记录的对端有效组 ID。
func (b *Binding) GID() uint32 { return b.gid }

// Starttime 返回连接建立时记录的进程启动时刻（clock ticks）。
func (b *Binding) Starttime() uint64 { return b.starttime }

// Exe 返回连接建立时记录的可执行镜像路径。
func (b *Binding) Exe() string { return b.exe }

// Comm 返回连接建立时 stat 中的 comm。该字段可被调用者任意改写，
// 禁止用于认证决策，只允许出现在诊断信息里。
func (b *Binding) Comm() string { return b.comm }
