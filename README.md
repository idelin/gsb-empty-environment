# 禁止代理转发选项

证书带no-agent-forwarding，会话仍打开了代理转发。该扩展存在时必须拒绝代理转发请求；没有该扩展才按服务配置决定。没有该扩展且服务允许时可以转发。由SSH服务调用可导入的Go库。带扩展时再请求一次转发。

## 策略

按以下顺序对每一次 `auth-agent-req`（代理转发）请求做判定，重复请求也要逐次判定：

1. 会话所用证书的 `Extensions` 中存在 `no-agent-forwarding` 键时，一律拒绝，
   与服务自身配置无关。判定只看键是否存在，值（通常为空字符串）不影响结果。
2. 不存在该扩展时，交由服务配置 `ServiceConfig.AllowAgentForwarding` 决定：
   允许则转发，否则拒绝。

## 用法

```go
import "example.org/netsec/q0313"

cfg := netsec.ServiceConfig{AllowAgentForwarding: true}

// extensions 为认证证书的 ssh.Certificate.Extensions。
accept, err := netsec.AgentForwardingReply(extensions, cfg)
if err != nil {
    // 以 false 回复 auth-agent-req，不要建立到 ssh-agent 的通道。
    var certErr *netsec.CertificateExtensionError
    if errors.As(err, &certErr) {
        // 证书 no-agent-forwarding 扩展导致的拒绝
    }
}
```

所有拒绝都可用 `errors.Is(err, netsec.ErrAgentForwardingDenied)` 统一判断；
服务配置导致的拒绝还匹配 `netsec.ErrAgentForwardingDisabled`。

## 在 SSH 服务中接入

`golang.org/x/crypto/ssh` 不会自动处理代理转发，服务需要在会话通道上处理
类型为 `auth-agent-req@openssh.com` 的 `ssh.Request`。在回复该请求前调用本库，
并把得到的布尔值作为回复内容，被拒绝时不要创建到客户端 agent 的通道：

```go
case req := <-sessionChannel.Requests():
    if req.Type == "auth-agent-req@openssh.com" {
        accept, _ := netsec.AgentForwardingReply(cert.Extensions, serviceCfg)
        _ = req.Reply(accept, nil)
        if !accept {
            continue // 即使同一通道再次收到该请求，也会再次走到这里并被拒绝
        }
        // accept 为 true 时才建立 agent 转发通道
    }
```

本库仅依赖 Go 标准库，可直接被 SSH 服务导入；判定无状态，因此同一会话上的
每一次转发请求（包括带扩展时再次请求转发）都会得到一致的拒绝结果。
