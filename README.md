# pbsched — Protobuf Wire 编解码 + 多租户 DRR 公平调度器

零外部依赖，仅使用 Go 标准库（兼容 Go 1.11+）。

## 组件

### `wire` — 手工实现的 Protocol Buffers 二进制 Wire-format 编解码器
- Varint 7 位压缩编解码（含 10 字节溢出检测）
- ZigZag 有符号整数转换（32/64 位）
- Fixed32 / Fixed64 小端定长编解码
- Length-delimited 字段与任意层级嵌套消息
- Packed Repeated 数组（varint / fixed32 / fixed64）
- 反射模式校验引擎：`wire:"kind,num[,packed][,required]"` struct tag 驱动，
  自动解码嵌套结构体、校验必填字段与 wire 类型匹配、安全跳过未知字段
- 全路径越界防护：任何截断/畸形输入返回 `*ParseError` / `*ValidationError`，绝不 panic

### `scheduler` — 多租户隔离与配额公平调度器
- 租户级独立有界队列（背压）与配额窗口（可注入时钟）
- Deficit Round Robin 加权公平排队：deficit 按 `weight × quantum` 每轮充值，
  大额请求跨轮累积 deficit，空闲租户 deficit 清零防囤积
- 租户增删互不影响（隔离）；单互斥锁保护全部状态，不派生后台 goroutine

## 运行

```sh
go test ./...        # 全部单元/边界/并发测试
go test -race ./...  # 竞态检测
go run ./cmd/demo    # 运行样例
```
