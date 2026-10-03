# wire-arbiter

零外部依赖的 TypeScript 实现，包含三部分：

1. **Protobuf 二进制 Wire-format 编解码器**（纯手工解析）：Varint 7 位压缩、ZigZag 有符号转换、Fixed32/64、Length-delimited 嵌套消息、Packed Repeated 数组，带安全越界防护（`TruncatedFieldError`）。
2. **反射模式校验引擎**：消息描述符（`defineMessage`）+ 运行时结构校验（`validateValue` / `assertValid`），报告字段路径级错误。
3. **多节点并发竞争与冲突仲裁器**：FIFO 公平异步互斥锁、全局有序多键锁（无死锁）、版本化 CAS 冲突仲裁、原子多键事务、乐观重试 `update`，并记录历史用于线性一致性验证。

## 环境要求

- Node.js **>= 23.6**（原生 TypeScript 类型擦除 + 内置 `node:test`，零依赖、零构建步骤）。

## 运行

```bash
node --version            # 确认 >= 23.6
npm test                  # 等价于 node --test 'test/**/*.test.ts'
npm run demo              # 等价于 node examples/demo.ts
```

## 结构

```
src/wire/varint.ts   Varint / ZigZag 编解码（64 位安全，溢出与截断检测）
src/wire/reader.ts   WireReader：tag、fixed32/64、length-delimited、子读取器、skip
src/wire/writer.ts   WireWriter：上述全部编码
src/wire/errors.ts   DecodeError / TruncatedFieldError / MalformedVarintError / ValidationError
src/schema.ts        反射描述符：defineMessage + 描述符自身校验
src/validate.ts      反射校验引擎：类型/范围/必填/深度/未知字段
src/codec.ts         基于描述符的通用消息编解码（嵌套、packed、未知字段策略）
src/arbiter.ts       AsyncMutex / KeyedLockManager / Arbiter（CAS、事务、历史记录）
test/                node:test 测试（含线性一致性回溯校验器 test/lin.ts）
examples/demo.ts     端到端运行样例
```

## 并发安全设计

- **原子性**：所有仲裁操作在单一 FIFO 互斥锁的临界区内完成；多键事务先校验全部 CAS 前置版本，任一失败则整体中止、状态不变。
- **线性一致性**：每个操作在调用与响应之间存在一个原子生效点（临界区内），历史记录 `invokedAt/respondedAt/seq`，测试用实时序一致性检查 + 回溯搜索验证。
- **无死锁**：多键锁按全局排序顺序获取；单锁临界区不嵌套。
- **无泄漏**：测试断言 `pendingCount/heldCount/waiterCount` 全部归零，所有 Promise 均 fulfilled。
