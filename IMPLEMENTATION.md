# 实现说明

纯标准库实现（构建环境无网络、无 k8s.io 模块），`Service` 类型按
`corev1.Service` 的受检字段逐项对齐，真实 webhook 中可直接用适配器转换或
替换为 `corev1.Service`。

## 文件

- `doc.go`：包文档与策略规则说明
- `types.go`：与 `corev1.Service` 对齐的本地类型
- `ipaddr.go`：严格 IP 解析（拒绝八进制/十六进制/前导零，`::ffff:` 映射先还原为 IPv4）
- `lenient.go`：ExternalName 场景的宽松 IP 字面量词法识别
- `policy.go`：保留集合、允许段判定、特殊地址检查、ExternalName 规则与入口

## 用法

```go
import netsec "example.org/netsec/q0678"

policy, err := netsec.NewPolicy(netsec.Options{
    AllowedCIDRs: allowedCIDRsForNamespace, // 调用方提供的命名空间可用地址段
    Reserved: netsec.ReservedSet{
        CIDRs: serviceCIDRsAndPodCIDRs,       // 集群地址段、Pod 段
        IPs:   append(nodeIPs, dnsServiceIP), // 节点地址、DNS 服务地址
    },
})
if err != nil { /* 配置非法，启动期 fail closed */ }

subresource := ""
if requestTargetsStatus {
    subresource = netsec.SubresourceStatus // status 子资源走同样规则
}
if err := policy.Validate(svc, subresource); err != nil {
    // 拒绝准入；err 是带字段路径的 *netsec.FieldError 聚合
}
```

## 验证

```sh
go vet ./...
go test ./...
```

测试覆盖节点地址、DNS 地址（IPv4/IPv6，含 status 路径）、`::ffff` 映射还原、
前导零/八进制/十六进制写法、回环/链路本地/组播/未指定地址、允许段外地址、
空允许段 fail-closed、status 子资源不可绕过，以及 ExternalName 的 IP 字面量与
跨命名空间 `.svc` / `.cluster.local` 指向。
