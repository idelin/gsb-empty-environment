# tx-invariant-store

事务性键值存储 + 跨字段不变量引擎。纯 TypeScript，零依赖，不采用任何响应式框架；
直接用 Node.js（>= 22.6，原生类型擦除）运行，测试基于内置 `node:test`。

## 设计要点

- **注册跨字段不变量**：`store.registerInvariant({ name, deps, check })`。
  `deps` 是廉价的依赖探测（被追踪的读取），`check` 是昂贵验证，抛错即拒绝提交。
- **提交前只检查受影响规则**：按“变更字段 ∩ 依赖边”索引，未命中的规则不执行。
- **失败返回涉及字段与操作**：`RuleFailure` 含 `fields`（规则实际依赖）、
  `relevantOps`（最小相关操作子集）、`counterexample`（可重放反例）。
- **禁止订阅后修正**：不存在“先提交再修补”的通道；验证全部发生在原子换出快照之前。
- **依赖可随条件分支改变**：每次提交先在事务草稿上重算每条规则的实际依赖
  （暂存的依赖图），再决定验证范围；拒绝的提交不会留下新依赖边。
- **共享纯计算**：`defineCompute` 定义的纯计算在同一草稿内记忆化，多条不变量复用一次求值。
- **小事务逐条全验对照**：操作数 ≤ `smallTxThreshold` 时，增量验证之外再全量验证，
  依赖声明遗漏也能被补获（`detectedBy: "exhaustive"`）。
- **拒绝不留痕迹**：草稿与暂存依赖图整体丢弃，状态快照与依赖图只在成功时原子替换。
- **操作可回放**：`Op` 是可序列化的 `set` 日志；`store.replay(ops)` /
  `store.replayCounterexample(ce)` 可复现结果。
- **读者隔离**：`store.read()` 只返回冻结的已提交快照，验证期间也读不到中间态。
- **规则抛错即失败**：`check` 抛出任何错误（不限于 `Violation`）都会拒绝提交，
  `kind` 标记为 `rule-error`。

## 运行

```sh
npm test     # node --test test/*.test.ts
npm run demo # node examples/demo.ts
```

## 运行样例（`npm run demo` 摘要）

```
=== 合法转账 alice->bob 20 ===
{ "ok": true, "version": 1, "verified": "exhaustive", ... }

=== 非法提交（余额为负） ===
{ "ok": false, "failures": [
  { "rule": "non-negative-balances", "kind": "violation",
    "message": "balance:alice 低于下界 0：-10",
    "fields": ["balance:alice", "balance:bob", "balance:carol"],
    "relevantOps": [{ "kind": "set", "field": "balance:alice", "value": -10 }, ...],
    "counterexample": { "baseVersion": 1, "ops": [...] },
    "detectedBy": "incremental" },
  { "rule": "total-conserved", ... } ] }
重放反例 -> 复现违例：balance:alice 低于下界 0：-10

=== 切 strict 且超限（动态依赖） ===
... "rule": "within-limit", "message": "balance:alice 超过 strict 限额 120" ...

=== 只改与 ledger 无关的字段（5 个操作，增量验证） ===
{ "ok": true, "verified": "incremental", "checkedRules": [] }
expensive-audit 本次执行次数：0（0 = 未被触发）

totalBalance 总求值次数： 5        # 多条不变量共享同一纯计算
操作日志在全新仓库回放： 成功，结果一致
```

## 测试结果（`npm test`）

```
✔ 合法提交成功，读者看到新快照
✔ 下界与总和约束同时检查，一次失败返回两条违例
✔ 失败返回涉及字段、最小相关操作子集与可重放反例
✔ 拒绝的提交不留新依赖边、不留半更新状态
✔ 依赖随条件分支改变：先在草稿上更新实际依赖再验证，不沿用旧依赖漏检
✔ 多条不变量共享纯计算：同一草稿内只求值一次
✔ 小事务逐条全验对照：依赖声明遗漏也能被全验补获
✔ 不相关字段不触发昂贵规则
✔ 规则自身抛错（非 Violation）使提交失败，kind 为 rule-error
✔ 读者不能看到事务中间态
✔ 状态操作可回放：同一操作日志在全新仓库得到相同结果
✔ 事务草稿 API：begin/set/commit 记录可序列化操作
✔ 首次提交验证全部规则（含未被变更字段触及的规则）
ℹ tests 13  ℹ pass 13  ℹ fail 0
```

## 文件结构

- `src/store.ts` — 引擎：Store / Transaction / 依赖图 / 共享纯计算 / 提交流水线
- `src/index.ts` — 公共导出
- `examples/demo.ts` — 余额场景运行样例（下界 + 总和约束 + 动态依赖 + 昂贵规则）
- `test/store.test.ts` — 13 个 `node:test` 用例
