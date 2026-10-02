# wirearb — Protobuf Wire 编解码器 + 多节点冲突仲裁器

零外部依赖，仅使用 Go 标准库（兼容 Go 1.11+）。

## 组件

### `wire` — 手工实现的 Protocol Buffers 二进制 wire-format 编解码器
- Varint：7 位/字节压缩编码，10 字节上限，溢出报 `ErrOverflow`
- ZigZag：有符号整数映射（`EncodeZigZag` / `DecodeZigZag`）
- Fixed32 / Fixed64：小端定宽编解码
- Length-delimited：嵌套消息、字符串、Packed Repeated 数组
- 全程边界检查：截断输入报 `ErrTruncated`，绝不越界、绝不 panic
- 哨兵错误：`ErrTruncated` / `ErrOverflow` / `ErrInvalidWireType` / `ErrInvalidFieldNumber`

### `arbiter` — 多节点并发竞争与冲突仲裁器
- `Propose` 授予严格递增的 fencing token（epoch），旧 token 立即失效
- `Commit` 仅在 token 仍为最新且持有者匹配时原子落账，否则拒绝
  （`ErrStale` / `ErrNotHolder`，经 `CommitError.Kind` 分类）
- 原子性：每次状态迁移在单把互斥锁内不可分割地完成
- 线性一致性：提交日志是严格全序，epoch 单调递增
- 无死锁：单锁、无锁序、无阻塞调用、不派生 goroutine
- 无竞态：全部共享状态仅在锁内访问，`-race` 验证通过

## 运行

```sh
go test -race -count=1 ./...   # 全部测试（含竞态检测）
go run ./cmd/demo              # 运行样例
```

## 测试覆盖

- `wire`：varint 边界值/截断/溢出、ZigZag 全表与扫描往返、fixed32/64、
  三层嵌套消息还原、packed varint/svarint/fixed32/fixed64、
  未知字段跳过、逐字节前缀截断安全性（不 panic、越界即报错）
- `arbiter`：顺序提交、epoch 单调、过期 token 拒绝、冒名节点拒绝、
  16 节点并发竞争统计一致性、日志线性一致性断言、
  fencing 竞争窗口、死锁看门狗、goroutine 泄漏断言、日志拷贝隔离
