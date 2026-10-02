# 多粒度锁（MGL）与意向锁调度器

TypeScript 实现的多粒度层次锁管理器，零第三方依赖，使用 Node.js 内置
`node:test` 测试与原生 TypeScript 类型擦除运行（Node ≥ 22.6）。

## 设计

- **锁层次**：数据库 → 表 → 行，资源键形如 `db:shop.table:orders.row:R1`。
- **锁模式**：`IS / IX / S / SIX / X`，标准相容矩阵（IX 与 IX 相容，IX 与 S/X 互斥等）。
- **意向锁协议**：`Transaction.lockRow()` 在行上加 S/X 前，自动在父级表与库上加
  IS/IX；`lockTable()` 同理先在库上加意向锁。
- **快速冲突判定**：行级冲突被意向锁“汇总”到表条目上，表级锁请求只与表条目
  当前的持有者集合做相容性检查，复杂度 O(持有者数)，**不遍历任何行级条目**
  （由 `stats.conflictChecks.row` 计数恒为 0 验证）。
- **调度器**：`acquire()` 返回 Promise，不可授予时进入 FIFO 等待队列；释放时
  按队首前缀连续唤醒相容等待者，保证公平、避免饥饿。
- **锁升级**：同一事务在同一资源上的模式叠加（如 S + IX = SIX）。
- **死锁检测**：等待图（wait-for）环检测，成环时后到的请求以 `DeadlockError` 中止。

## 运行

```bash
npm test     # Node.js 内置测试（node:test）
npm run demo # 验收场景运行样例
```

## 验收场景

事务 A 持有表 T 中行 R1 的 X 锁（表 T 自动带 IX）；事务 B 请求表 T 的整表 X 锁；
系统由 IX 与 X 互斥直接在表条目上判定并阻塞 B，行级条目检查次数为 0；
A 提交后调度器唤醒 B 授予整表 X 锁。对应测试见
`test/lock-manager.test.ts` 的“验收场景”用例，演示见 `examples/demo.ts`。

## 测试结果

```
✔ 相容矩阵：IX 与 X 互斥，IX 与 IX 相容
✔ 锁模式叠加：S + IX = SIX
✔ 行级 X 锁自动在表与库上加 IX 意向锁
✔ 行级 S 锁自动在表与库上加 IS 意向锁
✔ 验收场景：A 持行 R1 的 X 锁（表带 IX），B 的整表 X 请求被快速阻塞且不遍历行
✔ 表级 S 请求与行级 X 锁冲突（IS 与 S 相容但 IX 与 S 互斥）
✔ 两事务对不同行加 X 锁可并行（IX 与 IX 相容）
✔ 同一行上的 X 锁互斥，后者阻塞并在前者提交后获锁
✔ 表级 S 与行级 S 相容（IS 不妨碍 S）
✔ 锁升级：同事务先 S 后 IX 叠加为 SIX
✔ FIFO 公平调度：等待者按序唤醒
✔ 死锁检测：相互等待时抛出 DeadlockError
✔ 提交释放全部层级锁，空条目被回收
ℹ tests 13  ℹ pass 13  ℹ fail 0
```

（Node v23.9.0 实测通过。）

## 文件结构

- `src/lock-manager.ts` — 锁管理器、相容矩阵、意向锁协议、FIFO 调度与死锁检测
- `test/lock-manager.test.ts` — 13 个 `node:test` 用例
- `examples/demo.ts` — 验收场景的可运行演示
