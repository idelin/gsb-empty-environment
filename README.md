# 逻辑删除转物理归档流式分流器

业务逻辑标记处理中间件：拦截表中将 `is_deleted` 置为 1 的 Update 事件，
自动转换为下游物理归档库的 Insert 事件（归档包，含完整历史数据）与
只读查询库 / 搜索索引的 Delete 事件，支持跨异构存储系统的路由分发，
对业务无感知。仅使用 Go 标准库。

## 结构

- `splitter/event.go` — 变更事件模型与逻辑删除判定
- `splitter/splitter.go` — 分流中间件（单条处理 + 流式消费）
- `splitter/sink.go` — 异构存储 Sink 抽象、路由器与示例实现（冷存储 / 搜索索引 / 透传）
- `splitter/splitter_test.go` — go test 测试
- `cmd/demo/main.go` — 运行样例

## 运行

```sh
go test ./... -v   # 测试
go run ./cmd/demo  # 样例：UPDATE user SET is_deleted=1 WHERE id=100
```
