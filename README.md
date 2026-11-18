# SFTP扩展请求权限

SFTP 服务器对基本操作做了只读和路径限制，攻击者改走 SSH_FXP_EXTENDED 里的 posix-rename@openssh.com、hardlink@openssh.com、copy-data 或 fsetstat@openssh.com 绕过。为Go的 SFTP 服务端写可导入的请求授权包：每个扩展请求先映射为等价的基本权限（重命名需源目录与目标目录写权限，硬链接需源读权限与目标目录写权限，copy-data 需源读与目标写）再走同一套路径规范化与只读开关；未知扩展名一律返回 SSH_FX_OP_UNSUPPORTED，不因扩展名带 @ 域名而放行；规范化后越出根目录即拒绝，符号链接解析后的真实路径也须在根内（文件系统接口由调用方注入）。测只读模式下的各扩展、越根重命名、硬链接到无读权限的文件。

## 用法

```go
import "example.org/netsec/q0517"

authz, err := netsec.New(netsec.Config{
	Root:     "/var/sftp/root",
	FS:       myFS, // 实现 netsec.FS：EvalSymlinks / ResolveHandle / Access
	ReadOnly: true,
})

// 基本操作
if err := authz.CheckRead("/docs/a.txt"); err != nil { /* 返回 *netsec.StatusError */ }

// SSH_FXP_EXTENDED：先映射为等价基本权限，再走同一条检查链路
err = authz.CheckExtended(netsec.ExtendedRequest{
	Name:    netsec.ExtPosixRename,
	OldPath: "/docs/a.txt",
	NewPath: "/archive/a.txt",
})
```

## 安全语义

- 扩展 → 基本权限映射：`posix-rename` = 源目录写 + 目标目录写；`hardlink` = 源文件读 + 链接目录写；`copy-data` = 读句柄读 + 写句柄写；`fsetstat` = 句柄写。
- 只读模式在权限检查最前端拒绝所有写类操作（返回 `SSH_FX_PERMISSION_DENIED`）。
- 虚拟路径先词法规范化并做根目录包含检查；再经注入的 `FS.EvalSymlinks` 解析真实路径，对真实路径二次包含检查，符号链接指向根外同样拒绝。
- 句柄类扩展先经 `FS.ResolveHandle` 映射并验证宿主路径仍在根内。
- 未知名（包括伪造的 `xxx@openssh.com` 后缀）一律 `SSH_FX_OP_UNSUPPORTED`，不触发任何文件系统访问。
- 错误统一为带 SSH_FX_* 状态码的 `*netsec.StatusError`，可直接翻译为 SSH_FXP_STATUS 响应。
