Kubernetes `NetworkPolicy` 的默认隔离语义与叠加规则极易被写反：某命名空间下若没有任何策略选中 Pod `P`，则 `P` 的入站与出站均为默认全放行；一旦有任意策略选中了 `P` 且其 `policyTypes` 含 `Ingress`，`P` 立刻变为**入站默认拒绝**，但出站仍保持默认全放行（除非另有选中 `P` 的策略声明了 `Egress`！）；更微妙的是空规则列表 `ingress: []` 代表全部拒绝，而含单个空对象的 `ingress: [{}]` 代表放行全部入站；此外同一规则内 `from` 列表多项之间是逻辑或（OR），而单项内同时写 `namespaceSelector` 与 `podSelector` 则是逻辑与（AND），`ports` 与 `from` 之间也是逻辑与（AND）！为 CNI 数据面交付可导入的 Go NetworkPolicy 编译与五元组求值包。组件支持 IPv4 与 IPv6 双栈地址、多命名空间与多 Pod（每个 Pod 含标签、IP 及命名端口映射 `portName -> (containerPort, protocol)`——不同目标 Pod 同一端口名可映射到不同数字端口！）。规则：1）严格遵循 K8s `policyTypes` 推导（未显式写 `policyTypes` 时总是含 `Ingress`，仅当显式存在 `egress` 字段时才包含 `Egress`！）；2）实现精确 `ipBlock` 校验（校验 `except` 必须是 `cidr` 的合法子网，计算 `cidr \ union(except)` 前缀差集并支持 IPv4/IPv6）；3）编译多策略白名单并集，正确区分同条 `peer` 同时含 `podSelector`+`namespaceSelector`（AND）vs 两条独立 `peer`（OR）、省略 `namespaceSelector` 默认限定策略所在命名空间、`endPort` 闭区间范围（仅对数字端口生效，命名端口禁止配 `endPort`！）以及省略 `protocol` 默认 `TCP`；4）提供端到端连通性判定 `Evaluate(srcPodOrIP, dstPodOrIP, proto, dstPort)`——同时校验源端 `Egress` 隔离决策与目的端 `Ingress` 隔离决策，两者同时放行才算连通（且同一 Pod 回环流量按 K8s 规范总是放行！）。验收覆盖未选默认放行、单向 `Ingress` 隔离不误封 `Egress`、`ingress: []` vs `ingress: [{}]`、同项 AND vs 异项 OR、`ipBlock` 多重叠 `except` 差集、不同 Pod 同名命名端口解析及源 Egress+目的 Ingress 双侧联动判定。

## Go package

The repository root is an importable package named `networkpolicy`. It has no
third-party dependencies and targets Go 1.22+.

```go
cluster := networkpolicy.NewCluster()
cluster.AddNamespace(networkpolicy.Namespace{
    Name:   "prod",
    Labels: map[string]string{"env": "prod"},
})
cluster.AddPod(networkpolicy.Pod{
    Name:      "api",
    Namespace: "prod",
    Labels:    map[string]string{"app": "api"},
    IPs:       []string{"10.0.0.1", "fd00::1"},
    Ports: map[string]networkpolicy.ContainerPort{
        "http": {ContainerPort: 8443, Protocol: networkpolicy.TCP},
    },
})
cluster.AddPod(networkpolicy.Pod{
    Name:      "client",
    Namespace: "prod",
    Labels:    map[string]string{"role": "client"},
    IP:        "10.0.0.2",
})

policies := []networkpolicy.NetworkPolicy{{
    Name:        "api-ingress",
    Namespace:   "prod",
    PodSelector: networkpolicy.LabelSelector{"app": "api"},
    Ingress: []networkpolicy.IngressRule{{
        From: []networkpolicy.Peer{{
            NamespaceSelector: networkpolicy.LabelSelector{"env": "prod"},
            PodSelector:       networkpolicy.LabelSelector{"role": "client"},
        }},
        Ports: []networkpolicy.NetworkPolicyPort{{Name: "http"}},
    }},
}}

engine, err := networkpolicy.Compile(cluster, policies)
if err != nil {
    // malformed ipBlock, endPort, protocol, or port configuration
}

allowed, err := engine.Evaluate("prod/client", "prod/api", "TCP", 8443)
```

`Evaluate` accepts `Endpoint`, `Pod`, `*Pod`, `PodRef`, `netip.Addr`, or a
string. Strings are resolved first as an IP, then as `namespace/name`, then as
a unique Pod name. The destination port may be numeric or a named port on the
destination Pod. `AllowsIngress` and `AllowsEgress` expose the two independent
decisions; `Evaluate` requires both to allow, except for same-Pod loopback.

`PrefixDifference(cidr, except)` validates an `ipBlock` and returns the exact
non-overlapping prefix cover of `cidr \ union(except)`. `EffectivePolicyTypes`
exposes the `policyTypes` inference rule.
