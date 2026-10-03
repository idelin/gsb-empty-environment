# wireshard

零外部依赖的 Go 实现，包含两部分：

## `wire` — 纯手工 Protobuf 二进制 Wire-format 编解码器

- Varint 7 位压缩编码/解码（含 10 字节溢出防护）
- ZigZag 有符号整数映射（sint32/sint64）
- Fixed32 / Fixed64 小端定长编解码
- Length-delimited 嵌套消息（任意深度）
- Packed Repeated 数组（varint / sint / fixed32 / fixed64）
- 反射模式校验引擎：`Validate` 校验 `pb` struct tag 模式，
  `Unmarshal`/`Marshal` 按 tag 精确还原结构体字段
- 所有读取均做边界检查：字段截断返回 `*TruncatedError`（含偏移量），
  绝不 panic

Struct tag 格式：`` `pb:"<字段号>,<kind>[,packed][,repeated]"` ``，
kind ∈ `varint|sint|fixed32|fixed64|bytes|string|message`。

## `shard` — 分层分片与局部性感知路由引擎

- 固定数量虚拟分片（vshard），Rendezvous(HRW) 哈希定位副本集，
  成员变更时分片迁移量最小
- 副本集跨 zone 分散放置（层级感知容灾）
- 路由按局部性分级：same-node > same-rack > same-zone > remote，
  同级按实时负载（Acquire/Done 计数）打破平局，大幅降低跨节点/跨机房流量
- 全部热路径读锁/无锁，并发安全；路由统计使用原子计数器

## 运行

```sh
go test ./...        # 全部单元测试（边界 + 并发 + 泄漏断言）
go test -race ./...  # 竞态检测
go run ./cmd/demo    # 运行样例
```
