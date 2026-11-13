# 802.1X端口授权模式与搭车MAC违规

端口上先有一台合法设备通过认证，后面再接集线器挂上另一台，就可能搭着已授权状态通信。为Go交换机接入组件写可导入的端口授权判定包：single-host 只授权首个认证的 MAC，其他源 MAC 的帧触发违规动作（restrict 丢弃，shutdown 禁用端口）；multi-auth 每个 MAC 单独认证，未认证 MAC 的帧丢弃；multi-host 认证一个后放行所有，首个 MAC 老化或 EAPOL-Logoff 后整端口回到未授权。用第二个 MAC、logoff 和老化验证。

## 使用

导入 `example.org/netsec/q0822`，创建端口状态机后，把 EAP 认证、EAPOL-Logoff、老化事件送入端口，请求转发前用 `Evaluate` 判定：

```go
port := netsec.NewPort(netsec.SingleHost, netsec.Shutdown)
first := netsec.MustParseMAC("02:00:00:00:00:01")
second := netsec.MustParseMAC("02:00:00:00:00:02")

port.Authenticate(first)
decision := port.Evaluate(second)
// decision.Action == netsec.ActionShutdown，端口随即进入 Disabled 状态。

port.Logoff(first) // 或 port.AgeOut(first)
```

三种模式：

- `SingleHost`：仅首个认证 MAC 可转发；第二个源 MAC 触发 `Restrict`（丢帧）或 `Shutdown`（禁用端口）。
- `MultiAuth`：认证集合中的每个 MAC 单独放行，未认证 MAC 丢弃；`Logoff`/`AgeOut` 只移除对应 MAC。
- `MultiHost`：首个 MAC 认证后放行所有源 MAC；仅首个 MAC 的 `Logoff`/`AgeOut` 会关闭整个端口。

`Reset` 可清除认证状态并重新启用被 shutdown 的端口，之后需重新认证。所有方法可并发调用。
