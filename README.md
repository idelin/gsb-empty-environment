# pbwire — 零依赖 Protobuf Wire-Format 编解码 + 无锁并发队列（Go）

纯标准库实现，无任何外部依赖，兼容 Go 1.11+。

## 模块

- `wire/` — 手工编写的 Protocol Buffers 二进制 wire-format 编解码器
  - Varint 7 位压缩编码（含 64 位溢出检测，最多 10 字节）
  - ZigZag 有符号整数转换（`sint32/sint64`）
  - Fixed32 / Fixed64 小端定长编码
  - Length-delimited 嵌套消息、字符串、字节串
  - 反射解码引擎：`Unmarshal(data, &v)` 按 `protobuf:"<num>,<enc>[,packed][,req]"`
    结构体标签还原字段，支持多层嵌套消息、Packed/ unpacked Repeated 数组、
    未知字段跳过
  - 校验引擎：`req` 标记的必填字段缺失时返回 `*ValidationError`（含嵌套路径）
  - 安全越界防护：所有读取均边界检查，截断输入返回 `ErrTruncated`，绝不 panic
- `queue/` — 基于原子操作的无锁并发原语（无互斥锁、无 channel）
  - `BoundedQueue`：Vyukov 有界 MPMC 环形队列，每槽位序列号 + 单 CAS 认领
  - `Queue`：Michael-Scott 无界无锁链表队列（GC 下无 ABA 风险）
  - `Counter`：CAS 重试循环实现的共享计数器与 `Max`
  - 热路径计数器做 cache-line 填充，避免伪共享
- `cmd/demo/` — 可运行样例：手工编码嵌套消息 → 反射解码 → 截断/必填校验演示
  → 无锁队列 vs 互斥锁队列吞吐对比

## 运行

```sh
go test ./...          # 全部单元测试（边界 + 并发 + 泄漏断言）
go test -race ./...    # 竞态检测
go run ./cmd/demo      # 运行样例
```

## 样例输出

```
== protobuf wire-format demo ==
encoded 54 bytes: 08a946120564656c696e180d22037669702204727573682a04c8012746318fc2f5285cff58403a0e0a087368616e6768616910c09a0c
decoded: ID=9001 Buyer=delin Delta=-7 Tags=[vip rush] Amounts=[100 -20 35] Price=99.99 Addr={City:shanghai Zip:200000}
truncated stream -> wire: truncated input
missing required -> wire: missing required fields: Buyer, Addr

== lock-free MPMC queue demo ==
lock-free queue: 1600000 ops in 105.769417ms (15127246 ops/sec)
mutex queue:    1600000 ops in 567.464459ms (2819560 ops/sec)
```

无锁队列吞吐约为互斥锁队列的 5.4 倍（8 生产者 / 4 消费者，darwin/amd64）。

## 测试覆盖（29 项，全部通过）

- `wire`：varint 规范向量与往返、10 字节溢出、ZigZag 全边界（含 ±2^63）、
  fixed32/64 往返、逐字节截断扫描（每个前缀都不 panic 且安全报错）、
  超长 length 前缀防溢出、非法 wire type / 0 号字段拒绝、未知字段跳过、
  多层嵌套 + packed/unpacked repeated 还原、必填校验（含嵌套路径）、
  wire type 不匹配、非法解码目标
- `queue`：空队列出队、容量 2 的幂向上取整、满队列入队拒绝、FIFO 与
  环形回绕、交替入出队、8 生产者 × 4 消费者 exactly-once 投递
  （有界 + 无界各 16 万条，位图查重）、CAS 计数器并发求和与 Max、
  goroutine 泄漏断言（`runtime.NumGoroutine` 前后对比）
