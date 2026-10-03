# pbwire — 手工 Protobuf Wire 编解码 + 并发冲突仲裁器（零外部依赖）

仅使用 Go 标准库实现。

## 模块

- `wire/` — Protocol Buffers 二进制 wire-format 解析器
  - Varint（7 位压缩）、ZigZag 有符号整数、Fixed32/64、Length-delimited 嵌套消息
  - 反射解码：`protobuf:"<codec>,<fieldNum>[,packed]"` 标签映射结构体字段
  - Packed / 非 packed repeated 数组、未知字段安全跳过
  - 全程边界检查：字段截断返回 `ErrTruncated`，varint 超 64 位返回 `ErrOverflow`，绝不 panic
  - 反射校验引擎：`validate:"required,min=,max=,minlen=,maxlen="`，按字段路径聚合错误
- `arbiter/` — 多节点并发竞争与冲突仲裁器
  - 版本化 CAS（`Propose`）：每个 key 的已接受版本严格为 1..N 无间隙序列（线性一致）
  - 多 key 事务（`Transact`）：校验-提交两阶段，全成功或全回滚（原子性）
  - 锁按 key 全局排序获取，破除循环等待（无死锁）；不启动任何后台 goroutine（无泄漏）
- `cmd/demo/` — 运行样例：手工编码嵌套消息 → 反射解码 → 校验 → 8 节点竞争演示

## 运行

```sh
go test ./... -race -count=1 -v   # 测试（含竞态检测）
go run ./cmd/demo                 # 样例
```
