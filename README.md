# rib — 版本化 IPv4/IPv6 二进制前缀路由表 + 离散事件仿真

纯标准库实现（`netip`、`container/heap`、`hash/fnv` 等），无任何真实网络流量。

## 结构

- `prefix.go` — 严格前缀/地址解析：拒绝空白、zone、越界长度；IPv4-mapped
  IPv6 自动 unmap；主机位按策略处理（`RejectHostBits` 默认拒绝 /
  `MaskHostBits` 规范化掩码）。
- `route.go` — 路由条目：`Priority`（高者优先，同优先级按插入序）、
  `NextHops` 下一跳组（ECMP，按流哈希确定性选择）、`Tag`。
- `trie.go` — 路径压缩二进制 trie（Patricia）：插入时分叉分裂、删除后
  合并（无路由单子节点被拼接消除），支持 IPv6 非字节对齐前缀（/33、/61、
  /127…）。节点不可变，写时复制路径克隆，旧快照结构共享。
- `table.go` — 版本快照表：`Apply(ops...)` 将整批插删构建成新快照后用
  一次原子指针交换发布；任一 op 非法则整批拒绝、版本不变。读者经
  `Acquire()` 取得 `View`，无锁读取，永远只见单一版本，不见混合树。
- `sim/` — 离散事件模型：整数 tick 调度器（同 tick 按 FIFO）、
  存储转发链路（`Interval` 串行化 + `Delay` 传播，FIFO 排队）、
  按每报文快照查表转发的路由器。

## 语义要点

- 更具体前缀覆盖默认路由；删除后自动回退到较短前缀。
- 同前缀多路由按优先级排序，撤回最优路由即恢复次优路由。
- 查询位访问上界 = 地址宽度（32/128）：压缩路径上的比较区间望远镜式
  拼接，测试逐次断言计数不越界。
- 旧快照在读者持有 `View` 期间保持可用，全部 `Release` 后由 GC 回收
  （`TestSnapshotReclaim` 用 finalizer 验证）。

## 运行

```sh
go test ./...      # 全部测试
go run ./cmd/demo  # 演示
```

## 演示输出（go run ./cmd/demo）

```
== 1. strict parsing ==
reject host bits : prefix "10.0.0.1/8": host bits set (want "10.0.0.0/8")
mask policy      : 10.0.0.1/8 -> 10.0.0.0/8

== 2. atomic batch publish ==
published version 1
lookup 203.0.113.9                            -> 203.0.113.0/24         tag=primary   pri=100 bits=24
lookup 203.0.114.9                            -> 0.0.0.0/0              tag=default   pri=1   bits=23
lookup 2001:db8:e800:0007::1                  -> 2001:db8:e800::/61     tag=v6-nonbyte pri=50  bits=61

== 3. withdraw restores next-best ==
withdrew primary, version 2
lookup 203.0.113.9                            -> 203.0.113.0/24         tag=backup    pri=10  bits=24

== 5. discrete-event simulation (integer ticks) ==
tick 0   inject packet 1 at R1
tick 0   inject packet 2 at R1
tick 0   inject packet 3 at R1
tick 7   R2 delivered packet 1 (hops=2, born=0)
tick 9   R2 delivered packet 2 (hops=2, born=0)
tick 11  R2 delivered packet 3 (hops=2, born=0)
```

## 测试结果（go test -count=1 ./...，另 go test -race 亦通过）

```
--- PASS: TestParsePrefixStrict (0.00s)
--- PASS: TestParsePrefixMaskPolicy (0.00s)
--- PASS: TestParsePrefixMappedIPv4 (0.00s)
--- PASS: TestParseAddrStrict (0.00s)
--- PASS: TestAtomicPublish (0.00s)          # 并发读者从不见混合树
--- PASS: TestBatchRejectedAtomically (0.00s)
--- PASS: TestSnapshotReclaim (0.07s)        # 旧快照 GC 回收验证
--- PASS: TestVersionMonotonic (0.00s)
--- PASS: TestFuzzVsFullScan (2.01s)         # 400 批随机插删对照全前缀扫描
--- PASS: TestBitBoundDeepChain (0.03s)      # 位访问上界 32/128
--- PASS: TestSpecificOverridesDefaultAndFallback (0.00s)
--- PASS: TestWithdrawRestoresNextBest (0.00s)
--- PASS: TestNonByteAlignedIPv6 (0.00s)
ok      rib     2.234s
--- PASS: TestLinkTiming (0.00s)             # 串行化+传播时延精确到 tick
--- PASS: TestRerouteOnWithdraw (0.00s)      # 仿真中途撤路由切换次优/回退
--- PASS: TestDropNoRoute (0.00s)
--- PASS: TestECMPGroup (0.00s)
ok      rib/sim 0.139s
```
