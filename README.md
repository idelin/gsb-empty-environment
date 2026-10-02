# shardgw — 自适应局部感知分片网关

纯 Go 标准库实现的就近路由分片网关。

## 能力

- **就近访问**：内置 GeoDB（CIDR 最长前缀匹配）解析客户端来源 IP 的地理位置，
  按 Haversine 球面距离（或实测 RTT 兜底）为客户端选择最近的健康集群。
- **哈希分片**：集群内使用一致性哈希（128 虚拟节点）把客户端稳定映射到节点；
  节点宕机只迁移其承载的 key，集群级故障切换天然平滑。
- **同城 / 同局域网优先**：客户端 IP 命中节点声明的局域网网段时直达该节点；
  同城集群优先于跨城集群。
- **跨国拦截降级**：来源国与网关服务国不一致直接返回 `ErrCrossBorder` 降级；
  国内流量在任何情况下都不会落入海外集群。
- **秒级健康探测**：`StartProber` 以秒级间隔刷新节点健康位与实测 RTT，
  驱动自动故障转移；单次路由决策为微秒级。

## 结构

| 文件 | 职责 |
| --- | --- |
| `geo.go` | 经纬度位置、Haversine 距离、RTT 估算 |
| `ipdb.go` | CIDR → 地理位置库（最长前缀匹配）、局域网网段解析 |
| `ring.go` | 一致性哈希环 |
| `gateway.go` | 节点/集群模型、路由决策、跨国拦截、健康探测 |
| `cmd/demo/main.go` | 运行样例 |
| `gateway_test.go` | 验收测试 |

## 运行样例

```console
$ go run ./cmd/demo
== 1. 客户端就近路由（同城/同地域优先） ==
北京客户端                    client=111.195.1.10 loc=CN/北京 -> cluster=华北 node=north-3 dist=0km rtt~2ms [28.25µs]
深圳客户端                    client=14.17.22.1 loc=CN/深圳 -> cluster=华南 node=south-3 dist=0km rtt~2ms [3.583µs]

== 2. 华北集群全部宕机，平滑重选华南 ==
北京客户端(华北全宕)              client=111.195.1.10 loc=CN/北京 -> cluster=华南 node=south-2 dist=1943km rtt~21.4ms [2.084µs]
北京客户端(华北恢复)              client=111.195.1.10 loc=CN/北京 -> cluster=华北 node=north-3 dist=0km rtt~2ms [2.042µs]

== 3. 跨国流量精准拦截降级 ==
美国客户端                    shardgw: cross-border traffic intercepted, degraded (degraded=true)

== 4. 路由决策时延（秒级识别要求） ==
100000 次路由决策总耗时 352.904ms，平均 3.529µs/次
```

## 测试结果

```console
$ go test -v ./...
=== RUN   TestBeijingClientPrefersNorthChina
--- PASS: TestBeijingClientPrefersNorthChina (0.00s)
=== RUN   TestFailoverToSouthChinaWhenNorthDown
--- PASS: TestFailoverToSouthChinaWhenNorthDown (0.00s)
=== RUN   TestPartialOutageStaysInNorthChina
--- PASS: TestPartialOutageStaysInNorthChina (0.00s)
=== RUN   TestCrossBorderTrafficDegraded
--- PASS: TestCrossBorderTrafficDegraded (0.00s)
=== RUN   TestDomesticNeverRoutedOverseas
--- PASS: TestDomesticNeverRoutedOverseas (0.00s)
=== RUN   TestSameLANNodePreferred
--- PASS: TestSameLANNodePreferred (0.00s)
=== RUN   TestConsistentHashStability
    gateway_test.go:206: remapped 48/200 keys after losing north-1
--- PASS: TestConsistentHashStability (0.00s)
=== RUN   TestShenzhenClientPrefersSouthChina
--- PASS: TestShenzhenClientPrefersSouthChina (0.00s)
=== RUN   TestRouteDecisionLatency
    gateway_test.go:235: avg routing decision: 1.72µs
--- PASS: TestRouteDecisionLatency (0.02s)
=== RUN   TestProberDrivesFailover
--- PASS: TestProberDrivesFailover (0.02s)
=== RUN   TestHaversineKm
--- PASS: TestHaversineKm (0.00s)
=== RUN   TestGeoDBLongestPrefix
--- PASS: TestGeoDBLongestPrefix (0.00s)
PASS
ok  	shardgw	0.354s
```

验收边界对应：`TestBeijingClientPrefersNorthChina`（北京客户端优先华北）、
`TestFailoverToSouthChinaWhenNorthDown`（华北全宕平滑重选华南）、
`TestCrossBorderTrafficDegraded`（跨国拦截降级）、
`TestRouteDecisionLatency`（微秒级决策，远优于秒级识别要求）。
