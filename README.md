# SSH代理签名目的约束

堡垒机转发SSH代理时，攻击者可控制逐跳主机声明并请求受限密钥签名。为Go代理宿主实现目的约束校验包：注册密钥时保存允许的源到目标主机钥链；每次签名前验证完整转发链的主机公钥、用户名和最后目标，链中重复节点、缺失会话绑定或目的不符均拒绝。签名数据按SSH二进制串解析，禁止把普通数据冒充用户认证请求。用两条共享中间堡垒的链、篡改末跳和合法直连验证只有允许路径触发签名回调。

## 使用方式

注册密钥时给出完整的允许路径。`HostKey` 是 SSH wire-format 主机公钥
blob；首个没有 `HostKey` 的 hop 可表示运行 agent 的源主机（也可省略）。
`Username` 为空表示允许任意用户。

```go
agent := netsec.NewAgent()
err := agent.RegisterKey(userKeyBlob,
    netsec.Chain{
        {Hostname: "origin"},
        {Hostname: "bastion", Username: "ops", HostKey: bastionKey},
        {Hostname: "db-a", Username: "alice", HostKey: dbAKey},
    },
    netsec.Chain{
        {Hostname: "origin"},
        {Hostname: "bastion", Username: "ops", HostKey: bastionKey},
        {Hostname: "db-b", Username: "bob", HostKey: dbBKey},
    },
)
```

`Session.Bind` / `Session.BindMessage` 校验 `session-bind@openssh.com`
中主机私钥对 session identifier 的签名，并按到达顺序记录完整转发链。签名
请求通过 `Agent.HandleSignRequest` 处理；只有完整路径、末跳主机公钥、
用户名和 session identifier 全部匹配已注册约束时才会调用 `SignCallback`。

签名数据必须是 OpenSSH host-bound publickey 用户认证请求的 SSH 二进制
串布局（session identifier + `SSH2_MSG_USERAUTH_REQUEST`），普通挑战数据
不会被当作认证请求签名。转发链上的普通 `publickey` 请求（未携带末跳
host key）会被拒绝；单跳直连在明确注册的路径下仍可使用。

## 校验规则

- 已注册密钥之外、未注册路径、末跳 host key 或用户名不符：拒绝。
- 链中同一 host key / hostname 重复、存在环：拒绝。
- 没有 session binding，或签名数据中的 session identifier 与末跳绑定不符：拒绝。
- 普通数据、错误消息类型、非 SSH userauth 请求、多余尾随字段：解析阶段拒绝。
- 以上任何失败均发生在回调之前，签名回调调用次数保持为零或仅统计合法路径。
