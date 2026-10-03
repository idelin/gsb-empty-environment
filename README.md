# pbwire — 零依赖 Protobuf Wire-Format 编解码 + 无锁并发队列

纯 Go 标准库实现，无任何外部依赖。

## 组件

- **`wire/`** — 手工实现的 Protocol Buffers 二进制 wire-format 编解码器
  - Varint：7 位一组压缩编码，10 字节上限 + 64 位溢出检测
  - ZigZag：有符号整数变换（sint32/sint64）
  - Fixed32/Fixed64：小端定长编码
  - Length-delimited：字符串、字节串、任意层级嵌套消息、Packed Repeated 数组
  - 反射校验引擎：`Marshal`/`Unmarshal` 依据 `pb:"<num>,<kind>"` struct tag
    校验 wire type 并还原结构体字段；未知字段安全跳过
  - 安全越界防护：所有读取先验界，截断输入返回 `*TruncatedError`，绝不 panic
- **`queue/`** — 高性能无锁 MPMC 队列（Michael-Scott 算法）
  - 全程 `atomic.Pointer` + CAS，零互斥锁
  - 泛型实现，依赖 GC 回收节点，无需 hazard pointer
  - `Len()` 为原子计数器，静止时精确

## 运行

```sh
go run ./cmd/demo     # 运行样例：嵌套消息编解码 + 截断防护 + 队列压测
go test ./...         # 单元测试
go test -race ./...   # 竞态检测（arm64 / amd64 均通过）
```

## 测试结果

```
$ go test -race -count=1 ./...
ok  	pbwire/queue	2.165s
ok  	pbwire/wire	    1.650s
```

覆盖：varint 边界（0/127/128/MaxUint64/溢出/截断）、ZigZag 极值、
fixed32/64 截断、三层嵌套消息往返、packed repeated、逐字节截断防护、
wire-type 不匹配、未知字段跳过、8x8 生产者消费者 16 万件零丢失零重复、
排空后无残留节点（无泄漏）。
