# 内核消息凭据绑定

本地消息代理不能相信载荷里自报的用户身份。写供Go进程宿主调用的消息接收包，把内核附带的进程代次、用户与传入句柄绑定到同一消息；控制消息截断时整条拒绝并关闭已收到句柄。用分块传输替身测试正文完整但凭据缺失、多个凭据冲突及发送者退出后进程号复用。

## 用法

```go
transport := netsec.NewUnixTransport(conn) // 已连接的 *net.UnixConn
receiver := netsec.NewReceiver(transport)
msg, err := receiver.Recv() // 校验失败时句柄已被关闭
if err != nil {
    // ErrControlTruncated / ErrMissingCreds / ErrConflictingCreds / ErrIdentityChanged
}
defer msg.Close() // 关闭传入句柄
// msg.Payload 正文、msg.Creds 内核凭据（pid/uid/进程代次）、msg.FDs 传入句柄
```

安全策略：

- 身份一律取内核凭据（macOS `LOCAL_PEERCRED`/`LOCAL_PEERPID` + `proc_pidinfo` 启动时间作代次；Linux `SO_PEERCRED` + `/proc/<pid>/stat`），载荷自报身份被忽略。
- 控制消息截断（`MSG_CTRUNC`）、内核凭据缺失、多份凭据冲突、连接内身份变化（进程号复用）均整条拒绝，并关闭该消息已携带的全部句柄。
