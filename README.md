# 逻辑删除转物理归档流式分流器

业务逻辑标记处理中间件：拦截表中将 `is_deleted` 置为 1 的 `Update` 事件，
自动转换为下游**物理归档库（冷存储）的 Insert 归档包**与**只读查询库/搜索索引的 Delete 指令**，
支持跨异构存储系统的路由分发，业务无感知。

## 结构

- `event.go` — 变更事件模型与逻辑删除判定（前镜像不为 1、后镜像为 1，防重复归档）
- `parser.go` — `UPDATE ... SET ... WHERE id = N` 语句解析
- `splitter.go` — 分流器与路由器（`Sink` 抽象对接异构存储）
- `sinks.go` — 冷存储、搜索索引的内存实现
- `middleware.go` — 中间件：拦截业务 SQL，捕获前后镜像后交给分流器
- `cmd/demo/main.go` — 运行样例

## 运行

```sh
go run ./cmd/demo
go test ./... -v
```

## 验收边界

源库收到 `Update user set is_deleted=1 where id=100` 后：

1. 分流器向搜索索引发送删除 `ID=100` 的指令；
2. 同时向冷存储系统发送包含完整历史数据的归档包（`INSERT archive_user`）；
3. 业务侧只执行了一条普通 SQL，无感知。
