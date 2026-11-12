# 图形缓冲权限转递

图形缓冲区可经 Unix 套接字在进程间传递，攻击者会把只读缓冲再转交给更高权限的接收方，或在撤销后继续持有。为 Go 缓冲代理写供其调用的授权转递包，每次转递生成权限不超过上游的派生授予，不能扩大读写、再导出或映射权限；撤销按派生树级联生效，并回收待传输条目。用只读转成读写、跨级再导出、撤销后传输中的消息和多接收者验证。

## 包：`bufauth`

- `Perm`：`PermRead` / `PermWrite` / `PermReexport` / `PermMap` 四位权限掩码，用 `IsSubsetOf` 做不扩大校验。
- `Authority.Issue` 创建根授予；`Authority.Forward` 只能在父授予仍存活、持有 `PermReexport` 且新掩码是父掩码子集时派生子授予。
- `Authority.BeginTransfer` 复制 fd 并登记随机 token；`Authority.Claim` 凭 token 完成交接，撤销后的在途消息一律拒绝。
- `Authority.Revoke` 沿派生树级联失效，并关闭、移除所有待传输条目，回收结果由 `RevokeReport` 返回。
- `SendFD` / `ReceiveFD` / `SocketPair` 封装 `SCM_RIGHTS` 描述符传递。

典型流程与攻击拦截示例见 `bufauth/example_test.go`，安全模型见 `bufauth/doc.go`。
