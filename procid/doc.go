// Package procid 把本机 IPC 连接绑定到连接建立瞬间的调用者进程身份，
// 供特权守护进程的认证中间件使用。
//
// 背景威胁：
//   - 仅记录调用者 PID 是不安全的：原进程退出后内核会复用该 PID，
//     后来者可以冒名顶替。
//   - /proc/PID/stat 的 comm 字段位于圆括号内，且 comm 可以包含空格
//     与右括号 ')'，按空白或第一个 ')' 切分都会解析错字段。
//   - /proc/PID/exe 在可执行文件被替换（unlink 后重建同名文件）时，
//     readlink 会带上 " (deleted)" 后缀；进程被换、镜像被换都必须拒绝。
//
// 设计：
//   - 连接建立时用 SO_PEERCRED 取出内核填充的对端 PID/UID/GID，
//     再用 pidfd_open(2) 取得指向该 task 的稳定句柄。
//   - 从 /proc/PID/stat 最后一个 ')' 之后解析字段，starttime
//     （字段 22，自系统启动起的时钟滴答数）作为进程身份的一部分。
//   - 记录 starttime 与 /proc/PID/exe 解析结果；认证时重新读取并逐一比对，
//     starttime 变化说明 PID 已被复用，exe 带 deleted 或路径变化都拒绝。
//   - procfs 与 pidfd 全部通过 ProcFS、PidOpener 接口注入，测试用内存
//     假实现即可复现 PID 复用、伪造 comm、exe 替换等场景，无需 root。
//
// 典型用法（Linux）：
//
//	binder := &procid.Binder{Proc: procid.DefaultProcFS(), Opener: procid.DefaultPidOpener()}
//	conn, _ := listener.Accept()
//	binding, err := binder.BindConn(conn.(*net.UnixConn)) // 内部读取 SO_PEERCRED
//	if err != nil { conn.Close(); return }
//	defer binding.Close()
//	// ... 在认证中间件里：
//	if err := binding.Verify(); err != nil { /* 拒绝调用 */ }
package procid
