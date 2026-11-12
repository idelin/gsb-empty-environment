# 接入认证外内身份绑定

企业网络接入同时存在匿名外层身份与受保护内层身份，代理不能把别的会话内层成功套给当前端口。实现供Go接入宿主调用的EAP式状态包，内层结果绑定隧道导出值、交换摘要及物理端口实例，外层只用于路由不得成为授权主体。用两个并行隧道、同外层名字和端口重接测试成功证明不可跨会话拼接。

## 设计

- `Host`/`Port`（host.go）：接入宿主侧状态机。`Attach` 每次生成新端口实例；成功响应须通过 AuthMAC 验签、端口实例比对、交换摘要比对三重校验，再经密钥确认才授权。
- `Server`（server.go）：AAA 侧。外层身份仅经 `AddRealmRoute` 路由到策略档案；授权主体只来自验签后的内层身份。
- `Supplicant`（supplicant.go）：接入端模拟，供集成测试与联调。
- 绑定关系：内层成功 → `HMAC(hostKey, InnerID‖Exporter‖Digest‖PortID‖PortInst)`，其中 Exporter 派生自隧道双方随机数，Digest 以端口实例为起点吸收全部中继消息。

## 测试

```sh
go test ./...
```

- `TestParallelTunnelsSameOuterName`：两条并行隧道、同一外层名，各自独立授权。
- `TestCrossSessionSpliceRejected`：把会话 A 的成功响应/内层消息拼接到会话 B，均被拒绝。
- `TestPortReconnectInvalidatesBinding`：端口重接后重放旧成功与旧内层消息，均被拒绝。
- `TestOuterIdentityNotAuthorizationSubject`：外层谎称他人时，授权主体仍为内层身份。
- `TestSuccessBindingTamperRejected` / `TestConfirmRequiresTunnelExporter`：篡改绑定字段或伪造确认均被拒绝。
