# SMTP升级边界隔离

邮件代理在接受STARTTLS后仍把预读缓冲里的明文作为加密后的命令使用。写供Go中继调用的协议阶段组件，用可注入双工连接和TLS升级回调处理命令、响应及升级；明确升级成功前后清空明文解析状态，客户端流水线越过升级边界时拒绝连接。用可控分块流测试升级与后续命令同块、握手失败、服务器拒绝升级及半关闭，证明明文字节不会进入升级后会话。

## 组件

`starttls` 子包提供协议阶段引擎，不绑定具体网络栈，连接与 TLS 升级均通过接口注入：

- `DuplexConn`：可注入的双工字节流（`net.Conn`、`net.Pipe`、`*tls.Conn` 均满足）。
- `UpgradeFunc`：在原始连接上执行握手并返回升级后连接的回调。
- `Engine`：维护 `Plain → Upgrading → TLS → Closed` 阶段机，提供 `ReadLine`/`WriteLine`/
  `BeginUpgrade`/`Close`/`CloseRead`/`CloseWrite`，读写可并发，阶段转换全局串行。
- SMTP 辅助：`IsSTARTTLS`、`ResponseCode`、`IsPositiveCompletion`、客户端 `ClientStartTLS`
  与服务端 `ServerBeginUpgrade`；拒绝升级返回 `*UpgradeRejectedError` 且阶段保持 `Plain`。

## 升级边界保证

1. 所有读取只经过引擎自有的 `lineReader`，应用层无法直接读原始连接，明文无法绕过解析器。
2. `BeginUpgrade` 先检查解析器是否残留未消费字节：客户端把命令流水线到 STARTTLS 之后
   （包括“升级与后续命令同块”）时立即返回 `ErrPipelined`，清零缓冲并关闭连接，
   TLS 回调不会被调用。
3. 握手前清零并摘除明文解析器，阶段切到 `Upgrading`，期间应用读写快速失败
   （`ErrWrongPhase`）；握手成功后再次清零，并安装只绑定升级连接的全新解析器才进入 `TLS`。
4. 握手失败（含回调同时返回半成品连接）关闭原始连接与半成品连接，阶段落到 `Closed`。
5. 行协议严格：仅接受 CRLF 结束、内容为可打印 ASCII 的行；裸 LF、内嵌 CR、超长行、
   EOF 截断行一律拒绝，杜绝命令注入与半截明文被当成完整命令。

## 测试

- 可控分块脚本连接（`scriptedConn`）逐块投递、原始字节记录（`tapConn`）、模拟 TLS 连接，
  覆盖同块流水线、跨块预读、握手失败、服务端 554 拒绝、半关闭、干净 EOF、阶段守卫与
  缓冲清零（检查底层字节被置零）。
- `tls_integration_test.go` 用真实 `crypto/tls` + `net.Pipe` 验证真实握手、加密后命令收发，
  以及真实连接上的流水线攻击在 `tls.Server` 启动前被拒绝。
