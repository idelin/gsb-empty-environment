# seccomp通知双读防护

seccomp 用户通知监督者替沙箱进程处理 `openat` 时，路径指针指向被追踪进程的可写内存。攻击者可以在监督者读取后改写内容，或让通知在读取期间失效，从而造成 TOCTOU 或对失效通知放行。

本包的处理顺序固定为：

1. `SECCOMP_IOCTL_NOTIF_ID_VALID` 校验通知仍有效。
2. 用一次 `process_vm_readv` 把路径复制到监督者内存；短读不会重试，也绝不再次解引用原指针。
3. 再校验一次通知 ID，读取失败时也会执行这次校验。
4. 用被追踪进程的 `root` 和 `cwd` 视图解析并打开路径。
5. 用 `SECCOMP_IOCTL_NOTIF_ADDFD | SECCOMP_ADDFD_FLAG_SEND` 把打开得到的 fd 原子地注入目标进程并回复 fd。

监督者从不使用 `SECCOMP_USER_NOTIF_FLAG_CONTINUE`，因此内核不会拿可能已被改写的原路径重新执行 `openat`。

## 用法

```go
k := netsec.NewLinuxKernel(listenerFD)
h := netsec.NewHandler(k)

for {
	n, err := k.Receive()
	if err != nil {
		// listener 关闭等终止条件
		break
	}

	req, err := n.OpenatRequest()
	if err != nil {
		// 非 openat，交给其他处理器
		continue
	}

	res, err := h.HandleOpen(n, req)
	if errors.Is(err, netsec.ErrNotificationGone) {
		// 通知已失效，不应再做任何回复
		continue
	}
	_ = res
	_ = err
}
```

`Kernel` 接口把 ioctl、`process_vm_readv` 和文件系统视图集中到 `LinuxKernel`，测试中可用 fake 验证调用顺序和路径快照。

## 路径语义

- 相对路径从被追踪进程的 `cwd` 开始。
- 绝对路径放在被追踪进程的 `root` 内解析，不使用监督者自己的根目录。
- 路径中的 `..` 不在用户态做词法清理；交给 `openat2(RESOLVE_IN_ROOT)` 处理，避免改变 `symlink/..` 的语义，并由内核把 `..` 限制在进程根目录内。
- 额外使用 `RESOLVE_NO_MAGICLINKS`，防止路径中的 procfs magic link 逃逸根目录。

Linux 实现要求 `openat2`（Linux 5.6+）和支持 `SECCOMP_ADDFD_FLAG_SEND` 的用户通知（Linux 5.9+）。

## 测试

`handler_test.go` 覆盖：

- 读取过程中改写被追踪进程内存，确认只读取一次且打开的是第一次快照。
- 读取前/读取后通知失效，确认不打开、不注入、不回复。
- 合法相对路径（cwd）、绝对路径（root）、`dirfd`、`..` 保留语义和拒绝场景。
