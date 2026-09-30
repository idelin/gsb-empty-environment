# fwlab — 防火墙规则遮蔽/冲突分析与合成包离散事件仿真

纯 Go 标准库实现（`net/netip`、`container/heap`、`encoding/json`），无第三方依赖。
**所有数据包、队列、链路只存在于整数 tick 的离散事件模型中，程序不产生任何真实网络流量。**
合成反例包的源地址固定使用文档保留段（`192.0.2.0/24`、`2001:db8::/32`），仅用于模型内验证。

## 模型

- **规则**：`目的前缀 × 协议集合(tcp/udp/icmp/any) × 目的端口区间 × 动作(allow/deny)`，
  切片顺序即优先级，先匹配先生效，未命中默认 deny。
- **遮蔽（shadowing）**：对每条规则 j，用其匹配区域依次减去所有**同族**前规则的区域
  （区域 = 前缀集合 × 协议集合 × 端口区间的笛卡尔积，支持精确的 CIDR 减法与区间减法）。
  剩余为空 → j 完全被前规则覆盖，**不可达**（支持多条前规则**联合覆盖**）。
- **部分重叠**：j 与前规则相交但未被完全覆盖 → 只报告重叠；动作不同标记为**冲突**。
- **反例**：可达规则生成*见证包*（命中该规则）；被遮蔽规则生成*被截获示例包*（命中覆盖它的前规则）；
  重叠生成*交集示例包*（同时命中两条规则）。
- **域隔离**：IPv4 与 IPv6 规则/包严格分域，跨族永不匹配、永不遮蔽、永不报重叠。
- **离散事件仿真**：事件堆按 `(tick, seq)` 排序；流程为
  `到达 → 输入队列(容量有限,满则丢) → 匹配处理(serviceTicks) → 链路串行化(⌈size/速率⌉) → 传播时延 → 交付`，
  全部为整数 tick，结果确定性可复现。

## 运行

```sh
go run .                          # 内置演示规则集
go run . -v                       # 附带完整事件日志
go run . -rules examples/rules.json   # 从 JSON 加载规则与仿真参数
go test ./...                     # 运行全部测试
```

JSON 规则格式：`{"name","prefix","proto": "tcp"|["udp","tcp"]|"any","ports":"any"|"80"|"1-1024","action":"allow|deny"}`，
另有可选 `"sim"` 节配置 `serviceTicks/linkDelay/bytesPerTick/queueCapacity`。

## 运行样例（`go run .`，节选）

```
== 规则可达性 ==
[可达]   #0 web          allow tcp 10.0.0.0/8 ports=80-80
        见证包: pkt#0 192.0.2.1:49152 -> 10.0.0.0:80 tcp
[被遮蔽] #3 legacy-app   allow tcp 10.1.2.0/24 ports=8080-8080
        覆盖者: [1]，被截获示例包: pkt#0 192.0.2.1:49152 -> 10.1.2.0:8080 tcp
[被遮蔽] #7 lab-open     allow tcp 172.16.0.0/16 ports=any
        覆盖者: [5 6]，被截获示例包: pkt#0 192.0.2.1:49152 -> 172.16.0.0:0 tcp
[被遮蔽] #10 ghost        allow udp 192.0.2.0/24 ports=53-53
        覆盖者: [9]，被截获示例包: pkt#0 192.0.2.1:49152 -> 192.0.2.0:53 udp
== 部分重叠 ==
[冲突(动作不同)] #0(web) 与 #2(tcp-block)，交集示例包: pkt#0 192.0.2.1:49152 -> 10.0.0.0:80 tcp
[重叠] #2(tcp-block) 与 #9(v4-catchall)，交集示例包: pkt#0 192.0.2.1:49152 -> 10.0.0.0:0 tcp
...
== 离散事件仿真 ==
合成包 12 个，service=1 tick，linkDelay=2 tick，速率=64 B/tick，队列容量=8
-- 每包结果 --
tick=0    pkt#0   match      命中规则 #0(web) -> allow
tick=9    pkt#3   match      命中规则 #1(web-alt) -> allow      # legacy-app 的包被前规则截获
tick=21   pkt#7   match      命中规则 #5(lab-low) -> deny       # lab-open 的包被联合覆盖者截获
...
-- 规则命中计数 --
#3  legacy-app   hits=0
#7  lab-open     hits=0
#10 ghost        hits=0
默认拒绝=0 交付=5 丢弃=7 峰值队列=1
一致性校验通过：所有合成包的仿真命中结果与分析预测一致。
```

要点：`#3 legacy-app` 被 `#1 web-alt` 单规则遮蔽；`#7 lab-open` 被 `#5 lab-low` 与
`#6 lab-high` 两条规则**联合遮蔽**（端口区间 0-1023 + 1024-65535 拼成全覆盖）；
`#10 ghost` 被 `#9 v4-catchall` 遮蔽；v6 规则不受 v4 全拒规则影响。
被遮蔽规则在仿真中的命中计数恒为 0，与分析结论闭环一致。

## 测试结果（`go test -v .`，25 个用例全部通过）

```
--- PASS: TestShadowedBySingleRule     # 单规则完全遮蔽 → 不可达 + 截获示例包
--- PASS: TestShadowedByUnion          # 多条前规则联合遮蔽
--- PASS: TestPartialOverlapReported   # 部分覆盖只报重叠，动作不同标冲突
--- PASS: TestOverlapSameActionNotConflict
--- PASS: TestFamilyIsolation          # IPv4/IPv6 互不遮蔽、互不匹配
--- PASS: TestWitnessReachesRule       # 见证包/截获示例包与 Match 闭环
--- PASS: TestNoShadowNoOverlap
--- PASS: TestSubtractPrefix{Disjoint,Covered,Split,Deep,V6}
--- PASS: TestSubtractPorts
--- PASS: TestSubtractRegionUnionCover
--- PASS: TestIntersectRegionCrossFamily
--- PASS: TestSimAllowDeliveredDenyDropped
--- PASS: TestSimTiming                # tick 级时序：5→7→9→12
--- PASS: TestSimQueueFullDrops        # 队列容量与丢弃
--- PASS: TestSimDeterminism           # 两次运行日志逐条一致
--- PASS: TestSimMatchesAnalysis       # 仿真命中 == 分析预测
--- PASS: TestParsePorts
--- PASS: TestLoadConfig{OK,BadPrefix,BadAction,BadPorts}
ok      fwlab
```

## 文件

| 文件 | 说明 |
| --- | --- |
| `types.go` | 协议/动作/规则/包类型，匹配器，规则校验 |
| `region.go` | 区域代数：CIDR 前缀减法、端口区间运算、区域交/差、合成包采样 |
| `analyze.go` | 遮蔽与重叠分析、反例生成、报告输出 |
| `sim.go` | 离散事件仿真器（事件堆、FIFO 队列、服务台、链路） |
| `config.go` | JSON 配置加载与校验 |
| `main.go` | CLI、内置演示规则集、一致性校验 |
| `examples/rules.json` | 示例配置 |
| `*_test.go` | 全部测试 |
