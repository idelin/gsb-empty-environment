# procid — procfs 调用者身份绑定

供本机特权守护进程认证中间件使用的调用者身份绑定包。解决两个经典问题：

1. **PID 复用**：只记录 PID 时，原进程退出后内核可把同一 PID 分给新进程，
   后来者冒名顶替。本包在连接建立时用 `pidfd_open(2)` 拿到指向该 task
   的稳定句柄，并记录 `/proc/PID/stat` 的 `starttime`（字段 22）。
2. **伪造 comm 干扰 stat 解析**：comm 允许包含空格和 `)`，本包只在
   **最后一个 `)`** 之后按空白切字段，comm 内容不参与任何安全判定。

另外，`/proc/PID/exe` 出现 ` (deleted)` 后缀（镜像被 unlink/替换）
或归一化后的路径与连接时不一致，认证一律拒绝。

## 组件

| 文件 | 作用 |
| --- | --- |
| `binding.go` | 跨平台核心：`Binder` / `Binding`，绑定与每次请求的 `Verify` 逻辑 |
| `stat.go` | `ProcFS` 接口、stat 解析、deleted 判定（纯函数，跨平台可测） |
| `pidfd.go` | `PidFD` / `PidOpener` / `PeerCred` 接口定义 |
| `procfs_linux.go` | 真实 procfs 实现（`os.ReadFile` / `os.Readlink`） |
| `pidfd_linux.go` | 真实 pidfd 实现（`pidfd_open` / `pidfd_send_signal`，仅标准库） |
| `peercred_linux.go` | `SO_PEERCRED` 读取与 `Binder.BindConn` 便捷入口 |

procfs 与 pidfd 全部通过接口注入；测试使用内存假实现复现攻击场景，
不依赖 root，也能在非 Linux 开发机上运行。

## 守护进程接入示例

```go
package main

import (
	"errors"
	"log"
	"net"

	"example.org/netsec/q0186/procid"
)

func main() {
	// SO_PEERCRED 需要 SOCK_STREAM/SOCK_SEQPACKET 的 Unix 域套接字。
	ln, err := net.ListenUnix("unix", &net.UnixAddr{Name: "/run/myd.sock", Net: "unix"})
	if err != nil {
		log.Fatal(err)
	}

	binder := &procid.Binder{
		Proc:   procid.DefaultProcFS(),
		Opener: procid.DefaultPidOpener(),
	}

	for {
		conn, err := ln.AcceptUnix()
		if err != nil {
			log.Print(err)
			continue
		}
		go handle(conn, binder)
	}
}

func handle(conn *net.UnixConn, binder *procid.Binder) {
	// 必须在 Accept 后立即绑定，不要先处理请求数据。
	binding, err := binder.BindConn(conn)
	if err != nil {
		log.Print("bind rejected: ", err)
		conn.Close()
		return
	}
	defer binding.Close()

	for req := range readRequests(conn) {
		// 每次敏感请求都重新核验，不能只信任 Accept 时的缓存结论。
		if err := binding.Verify(); err != nil {
			if errors.Is(err, procid.ErrPIDReused) ||
				errors.Is(err, procid.ErrProcessExited) ||
				errors.Is(err, procid.ErrExeDeleted) ||
				errors.Is(err, procid.ErrExeChanged) {
				log.Print("caller identity invalid, closing: ", err)
				return
			}
			log.Print("verify error: ", err)
			return
		}
		serve(req, binding.UID())
	}
}
```

## 认证时刻的判定顺序

1. `pidfd_send_signal(fd, 0)`：原 task 已退出 → `ErrProcessExited`。
2. 重读 stat，比对 `starttime`：不同 → `ErrPIDReused`；stat 内 PID
   与请求 PID 不符同样按复用处理。
3. 重读 exe：带 ` (deleted)` → `ErrExeDeleted`；归一化路径不同
   → `ErrExeChanged`。

三步全过才放行。任何读取错误都按失败处理，绝不 fail-open。

## 测试覆盖

- `TestForgedComm`：comm 含空格与多个右括号，解析仍取到正确 starttime；
  认证不依赖 comm。
- `TestVerifyRejectsExitedProcess` / `TestVerifyFullPIDRecycling`：
  进程退出与完整 PID 回收链被 pidfd 拦下。
- `TestVerifyRejectsReusedPIDByStarttime` / `TestVerifyRejectsStatPIDMismatch`：
  PID 复用与半回收竞态由 starttime/PID 比对拦下。
- `TestVerifyRejectsDeletedExe` / `TestVerifyRejectsChangedExe` /
  `TestBindRejectsDeletedExeAndClosesFD`：exe 被替换在绑定和认证两端都拒绝。
- `TestBindAndVerifyLegitimate`：合法进程绑定、字段落档、重复认证通过。
- Linux 集成测试（`*_linux_test.go`）：真实 `SO_PEERCRED` + 真实 pidfd
  + 真实 `/proc/self`，并验证子进程退出后 pidfd 探活翻转。

## 安全边界

- 本包证明的是“连接对端仍是连接建立时的那个 task、镜像未被换”，
  不替代 UID/GID 能力判断或业务层授权；`PeerCred.UID/GID` 一并落档供上层使用。
- `pidfd` 要求内核 ≥ 5.3（pidfd_open）。更老的内核需升级，本包不提供
  退回纯 PID 方案——纯 PID 无法防复用。
- 容器内若 procfs 挂载在非标准位置，用 `NewLinuxProcFS(root)` 注入。
