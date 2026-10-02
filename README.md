# protosaga

零外部依赖（仅 Go 标准库）的两个组件：

## 1. `wire` — 手工实现的 Protocol Buffers 二进制 Wire-format 编解码器

- Varint：7 位一组、MSB 续位标志，最长 10 字节，超限报 `ErrOverflow`
- ZigZag：`sint32/sint64` 有符号映射（`EncodeZigZag` / `ReadZigZag`）
- Fixed32/Fixed64：小端定长读取
- Length-delimited：字符串、字节、嵌套消息、Packed Repeated 数组
- 反射模式校验引擎：`Schema`/`Field` 描述结构，`Validate`/`Decode` 校验
  wire 类型、必填字段、packed 对齐，并递归校验多层嵌套消息
- 安全越界防护：所有读取先查边界，截断输入返回 `ErrTruncated`，绝不 panic

## 2. `saga` — 反向补偿与事务回滚状态编排

- 步骤正向执行，失败时对已完成步骤按**严格逆序**补偿（Saga 模式）
- 状态机：`Pending → Running → Completed | Compensating → Compensated | Failed`
- 幂等：同一事务 ID 重放直接返回已存结果，动作/补偿各自最多执行一次
- 并发安全：同事务 ID 的并发调用等待首个执行完成并重放结果
- 最终一致：补偿失败不中断回滚，错误聚合上报，状态置 `Failed`
- 无泄漏：同步执行，不派生 goroutine

## 运行

```sh
export GO111MODULE=on
go test -race ./...   # 全部测试（含并发与竞态检测）
go run ./cmd/demo     # 运行样例
```
