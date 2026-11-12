# EAP-TLS 1.3成功帧承诺

EAP-TLS 1.3服务端若在TLS完成前发出EAP-Success，攻击者可伪造该消息使客户端以为认证已结束。为Go 802.1X接入组件实现客户端状态包，供EAP方法回调调用：收到最终握手消息并完成空的commitment消息后才接受EAP-Success；恢复握手须取回原始用户身份并重新查询授权。用提前Success、缺失commitment和身份被撤销后的恢复验证。

## 实现

`eaptls` 子包实现 RFC 8996 §2.5 的客户端状态机，EAP 方法通过回调驱动：

1. `New(Config)` 注入 `IdentityProvider`（取原始用户身份）与 `Authorizer`（授权查询）。
2. `Begin(ctx)` 取回身份并完成首次授权，状态进入 `running`。
3. `HandleRequest` 解析 EAP-TLS 请求并重组分片；明文 Finished 记录会自动标记最终握手，加密栈可显式调用 `MarkFinalHandshake`。
4. `Commitment` 仅在最终握手消息已确认后返回空的 EAP-TLS 响应（`EAP-Response/EAP-TLS`，无 TLS 数据），状态进入 `committed`。
5. `HandleSuccess` 仅在 `committed`/`succeeded` 状态接受 EAP-Success，否则返回 `ErrEarlySuccess`；标识符不匹配返回 `ErrIdentifierMismatch`。
6. `Resume(ctx)` 先清除旧握手进度，再重新取回原始身份并重新授权：身份变化返回 `ErrIdentityMismatch`，身份被撤销等授权失败原样向上返回，恢复期间收到的 Success 仍被拒绝。

### 安全保证

- 提前 Success：TLS 未完成、最终握手未到、或未发送 commitment 时，EAP-Success 一律拒绝且不改变状态，握手可继续。
- 缺失 commitment：未确认最终握手时 `Commitment` 返回 `ErrFinalHandshakeNotReceived`，不会发出空承诺帧。
- 恢复重查：每次 `Resume` 都重新调用身份提供者与授权器，撤销身份无法恢复；恢复后的握手需重新走完整承诺流程。

### 验证

```sh
go test -race -cover ./...
```

测试覆盖提前 Success（三个时序点）、缺失 commitment、身份撤销后的恢复拒绝、撤销解除后的完整恢复、分片重组中的注入 Success、标识符不匹配与终态防御等场景。
