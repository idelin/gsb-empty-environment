# 自适应局部感知分片网关（geogw）

纯 Go 标准库实现。维护节点地理经纬度与 IP 网段，根据客户端来源 IP 定位，
按物理距离 / 实测 RTT 将请求经一致性哈希路由至同城 / 同区域节点，跨国流量精准拦截降级。

## 路由层级

1. **同城**：客户端城市与节点城市一致，直接哈希到同城节点（RTT 保底 1ms）。
2. **同区域**：同城无健康节点时，路由至同大区（同局域网）节点。
3. **同国就近**：区域内全宕机时，按 Haversine 球面距离选最近集群，再哈希到节点。
4. **跨国拦截**：客户端定位在海外，或境内全宕机只剩海外节点时，返回 `ErrCrossBorder` 并降级，绝不跨境转发。

## 关键机制

- **地理定位**：节点 CIDR 构成定位库，最长前缀匹配客户端 IP → 国家/区域/城市/经纬度。
- **RTT 估算**：实测 RTT（健康探测回填）优先；否则按距离推算（经验值 100km/ms）。
- **一致性哈希**：候选节点 + 虚拟节点成环，key 稳定命中；节点增删仅迁移约 1/N 的 key，实现平滑重选。
- **秒级健康检查**：`WithProber` 注入探测器，默认 1s 周期，秒级发现宕机并切换。

## 运行

```sh
go test ./... -v   # 测试
go run ./cmd/demo  # 演示
```

## 测试结果（go test）

```
--- PASS: TestRouteBeijingClientToNorthCluster      北京客户端 -> 华北集群（同城，RTT<5ms）
--- PASS: TestFailoverToSouthWhenNorthDown          华北全宕机 -> 平滑重选华南（同国就近）
--- PASS: TestCrossBorderInterceptedAndDegraded     海外客户端 -> ErrCrossBorder 降级
--- PASS: TestDomesticOutageNeverCrossesBorder      境内全宕机 -> 拦截降级，不跨境转发
--- PASS: TestConsistentHashStabilityAndSmoothness  同 key 稳定；摘 1/2 节点迁移 279/500 key
--- PASS: TestHealthProbeDetectsFailureWithinASecond 秒级发现故障并转移
--- PASS: TestHaversineAndRTT                       距离与 RTT 估算
--- PASS: TestLocateLongestPrefixMatch              最长前缀匹配定位
ok  geogw
```

## 演示输出（go run ./cmd/demo）

```
== 1. 北京客户端正常路由（期望：华北集群） ==
   key="order-1001" -> 节点=cn-north-2 集群=华北 层级=同城 距离=0km 估算RTT=1ms
== 2. 华北集群全部宕机，秒级重选（期望：华南集群） ==
   key="order-1001" -> 节点=cn-south-1 集群=华南 层级=同国就近 距离=1943km 估算RTT=25ms
== 3. 海外客户端访问（期望：跨国拦截降级） ==
   [降级] key="order-2001" 原因=客户端位于 US，跨国流量精准拦截降级
```
