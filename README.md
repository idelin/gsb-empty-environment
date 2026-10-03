# pbsaga — Protobuf Wire 解析器 + Saga 反向补偿事务编排（零依赖 Go）

仅使用 Go 标准库，包含两个组件：

## 1. `wire` — 手工实现的 Protocol Buffers 二进制 Wire-format 编解码器

- **Varint**：7 位压缩编码，最多 10 字节；第 10 字节载荷超过 1 位即报 `ErrOverflow`
- **ZigZag**：sint32/sint64 有符号映射（`EncodeZigZag64` / `DecodeZigZag64`）
- **Fixed32 / Fixed64**：小端定长读写
- **Length-delimited**：嵌套消息（`DecodeMessage`）与 Packed Repeated 数组
  （`DecodePackedVarints` / `DecodePackedFixed32` / `DecodePackedFixed64`）
- **安全越界防护**：所有读取均做边界检查，截断输入返回 `ErrTruncated` /
  `ErrLengthOverflow` 等类型化错误，绝不 panic、绝不越界读

## 2. `saga` — 长生命周期流程的失败逆向补偿与事务回滚状态编排

- 步骤顺序执行；任一步失败或 context 取消时，对已完成的步骤按**严格逆序**补偿
- **幂等**：同一事务 ID 的 Action/Compensation 最多执行一次；
  重复/并发调用 `Run` 等待在途执行并返回相同结果
- **最终一致性**：补偿全部成功 → `ROLLED_BACK`；补偿失败不中断其余补偿，
  聚合后进入 `FAILED` 等待人工修复
- **并发安全**：全部状态由互斥锁保护；引擎不启动任何后台 goroutine（无泄漏）
- 状态机：`RUNNING → COMMITTED | COMPENSATING → ROLLED_BACK | FAILED`
- 每次执行产生有序审计日志（`Events()`）

## 运行

```sh
go test -race -count=1 -v ./...   # 全部测试（含竞态检测）
go run ./cmd/demo                 # 端到端示例
```

## 测试覆盖

- `wire`：varint 往返/截断/溢出、ZigZag 边界（MinInt64 等）、fixed 截断、
  三层嵌套消息解码、packed 数组、**逐字节截断防护**（每个前缀不 panic、
  切断尾字段必报错）、非法 tag、超长 length 前缀
- `saga`：提交路径、逐步失败表驱动回滚、逆序补偿断言、补偿失败仍完成其余补偿、
  context 取消触发回滚、顺序/并发幂等重跑、64 并发事务、混合结果并发、
  goroutine 泄漏断言（`runtime.NumGoroutine`）、审计日志顺序
