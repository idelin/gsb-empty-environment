# Service外部地址劫持准入

集群里有创建 Service 权限的租户，在 externalIPs 里写别人的服务地址或 DNS 服务器地址，就能把集群内发往这些地址的流量引到自己的 Pod，实施中间人攻击。给Go准入控制器实现可导入的 Service 外部地址准入包，输入 Service 对象、调用方给出的该命名空间可用地址段列表和集群保留地址集合（集群地址段、Pod 段、节点地址、DNS 服务地址）：externalIPs 与 status.loadBalancer.ingress 中的 ip，每一项都必须是合法的 IPv4 或 IPv6 地址，八进制、十六进制与带前导零的写法一律拒绝，IPv4 映射的 IPv6 地址（::ffff:a.b.c.d）先还原为 IPv4 再判定；必须落在该命名空间可用地址段内，且不得落入保留集合、回环、链路本地与组播段；type 为 ExternalName 时，externalName 不得是 IP 字面量，也不得以 .svc 或 .cluster.local 结尾而指向别的命名空间的服务；status 子资源的更新按同样规则判定，不得绕过。用节点地址、DNS 地址、::ffff 映射、前导零写法和 ExternalName 指向他人服务验证。
