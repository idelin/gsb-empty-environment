# SSI 防异常器（Serializable Snapshot Isolation）

在快照隔离（SI）之上实现可串行化快照隔离：基于 **SIREAD 锁** 追踪
rw-antidependency（读写反依赖）边，检测**危险结构**（dangerous structure）
`T_in -rw-> T_pivot -rw-> T_out`（T_out 最先提交），在写倾斜异常成环前夕
精准中止导致成环的事务，保证完全可串行化。

## 算法要点

- **MVCC 快照隔离**：每个事务在 `begin()` 时获取快照时间戳，读操作只见
  快照前已提交的版本；写操作缓存在私有 write-set，提交时生效。
- **写-写冲突**：先提交者获胜（first-committer-wins），后写者中止。
- **SIREAD 锁**：读操作在元组上留下 SIREAD 标记（提交后仍保留）；
  并发事务的写撞上他人 SIREAD 锁时建立 `reader -rw-> writer` 边；
  读到对自己快照不可见的新版本时建立 `reader -rw-> writer` 边。
- **危险结构判定**：某事务同时具有 rw 入边与 rw 出边，且存在已提交的
  出边目标 T_out，使得入边来源不早于 T_out 提交 —— 即 T_out 在三者中
  最先提交，满足写倾斜成环条件。
- **精准中止**：在建边时与提交时检查；枢纽事务仍活跃则中止枢纽，
  枢纽已提交则中止当前事务。无环事务不受任何影响（无不必要中止）。

## 运行

需要 Node.js >= 22.6（利用原生 TypeScript 类型擦除，无需编译与依赖）。

```bash
npm test       # Node.js 内置测试运行器
npm run demo   # 写倾斜对比样例（SI vs SSI）
```

## 验收场景

账户 A=100，B=100，约束 A+B>=0。T1 读 A、B 并扣 A；T2 并发读 A、B 并扣 B。
两个事务各自在快照内校验约束均通过。

- **纯 SI**：双重扣款均提交，A+B 变负，约束被破坏。
- **SSI**：识别出 `T1 -rw-> T2 -rw-> T1` 危险结构，T1 正常提交，
  T2 在提交时被精准中止，约束保持。

> 注：扣款额需 >100 才能使双重扣款后 A+B 真正为负（样例与主测试取 150）。
> 取规范原始数字 80 时最终 A+B=40 不违约，但 rw-反依赖环相同，
> SSI 同样中止 T2（见测试“规范原始数字（各扣 80）”）。

## 文件结构

- `src/ssi.ts` — SSI 引擎（`SsiDatabase` / `Transaction` / `AbortError`）
- `test/ssi.test.ts` — Node.js 内置测试（11 个用例）
- `examples/write-skew.ts` — SI 与 SSI 的对比运行样例
