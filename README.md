# pairwise — Protobuf Wire 编解码 · 反射校验 · 分层分片路由

零外部依赖（仅 Go 标准库），Go 1.11+。

## 组件

- `wirefmt/` — 纯手工 Protocol Buffers 二进制 wire-format 解析器/编码器。
  Varint（7 位分组）、ZigZag 有符号整数、Fixed32/64、嵌套
  Length-delimited 子消息、Packed Repeated 数组；所有读取均做边界检查，
  字段截断返回 `ErrTruncated`，绝不越界。
- `validate/` — 反射模式校验引擎。按 `validate:"required,min=1,max=64"`
  结构体标签递归校验嵌套结构体与切片，聚合全部违例（含点分字段路径）。
- `router/` — 分层分片与局部性感知路由引擎。key 经哈希映射到固定数量的
  虚拟分片，分片通过加权 Rendezvous(HRW) 哈希分配到节点；增删节点只迁移
  约 1/n 的分片，大幅降低跨节点通信。路由按 同可用区 > 同地域 > 远端
  排序候选节点。`sync.RWMutex` 保护，无后台 goroutine，无泄漏。
- `cmd/demo/` — 端到端样例：手工编码多层嵌套+Packed 消息 → 解析还原
  结构体 → 反射校验 → 截断防护演示 → 局部性感知路由与扩容迁移统计。

## 运行

```sh
go test -race -count=1 ./...   # 全部测试（含竞态检测）
go run ./cmd/demo              # 运行样例
```

## 测试结果（本机 Go 1.11.4, darwin/amd64）

```
ok  pairwise/router     # 分布均衡/加权/最小迁移/局部性/并发(-race)/无泄漏
ok  pairwise/validate   # required/min/max/len/嵌套路径/聚合错误
ok  pairwise/wirefmt    # varint/zigzag/fixed/嵌套/packed/截断防护
```

关键不变量由测试断言：

- 每个可能截断点（0..len-1）解析均安全报错、无 panic（`TestTruncationSafety`）。
- 扩容新增节点时，分片只在「旧节点 → 新节点」方向迁移，旧节点之间零迁移，
  迁移量 ≈ 1/(n+1)（`TestMinimalMovementOnAdd/OnRemove`）。
- 路由 rank0 恒为分片属主，fallback 按局部性单调排序（`TestLocalityPreference`）。
- 读写并发压测在 `-race` 下通过；`runtime.NumGoroutine` 前后一致，严格无泄漏。
