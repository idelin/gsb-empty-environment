# OWE过渡模式降级防护

OWE过渡模式靠开放SSID与隐藏OWE SSID的互指元素关联，恶意AP可仿冒开放网络，让客户端停留在无加密网络。为Go客户端连接组件实现扫描结果选择包，供漫游决策调用：开放BSS与OWE BSS的BSSID、SSID须双向一致才升级；单向声明、BSSID不符或OWE被拒时，不得悄悄回落开放连接，除非策略显式允许。用伪造元素、单向引用和合法成对网络验证。

## 选择规则

- 仅当开放BSS与OWE BSS各自携带Transition Mode元素，且互相指向对方时，才升级到OWE。
- 互相指向的BSSID必须等于扫描结果中对方的BSSID；SSID声明必须与对方实际SSID一致。
- 隐藏OWE SSID可以为空，但开放BSS必须声明非空的OWE SSID；隐藏SSID不能绕过反向引用检查。
- 单向引用、伪造目标、BSSID/SSID不符、元素畸形或重复冲突均默认阻断，不连接开放网络。
- OWE关联被拒绝后可通过`Selector.RejectOWE`记录；默认阻断，`Policy.AllowOpenFallback`显式开启后才返回开放BSS，并设置`Decision.Fallback`。
- 零值`Policy`不会选择任何开放BSS。纯开放网络需`AllowOpenWithoutOWE`或`AllowOpen`。

## API

```go
selector := netsec.NewSelector(netsec.Policy{})
decision := selector.Select(scanResults)
switch decision.Action {
case netsec.ActionConnectOWE:
    // decision.Pair is a verified bidirectional transition pair.
case netsec.ActionConnectOpen:
    // Only possible with an explicit policy; decision.Fallback marks downgrade.
case netsec.ActionBlock:
    // Do not connect to the open BSS.
}

// After an OWE association failure:
_ = selector.RejectOWE(oweBSSID)
decision = selector.Select(scanResults)
```

无状态调用可使用`Select(scan, policy)`或`SelectWithRejected(scan, policy, oweBSSID)`。原始信息元素可通过`ParseTransitionIEs`解析，`BuildTransitionElement`用于构造测试元素。

## 验证

```sh
go test -race ./...
```
