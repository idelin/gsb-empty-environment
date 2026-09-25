# json-fix-engine

对 JSON 兼容树按**前置条件 + 优先级**应用修复规则的引擎：冲突即停并列证据、
逐步记录可逆修改、达到不动点或耗尽步数预算时终止。零依赖 TypeScript，
使用 Node.js 内置测试运行器验证（Node ≥ 22.6，类型剥离直接运行 `.ts`）。

## 运行

```sh
npm test      # node --experimental-strip-types --test test/engine.test.ts
npm run sample
```

## 需求映射

| 需求 | 实现 |
| --- | --- |
| 前置条件不满足不得执行 | 每条匹配携带 `expect` 守卫，引擎在每轮应用前重新校验（`src/engine.ts` `#checkPrecondition`），不满足则记入 `skipped` 并跳过 |
| 按优先级应用 | 规则按 `priority` 降序、`id` 字典序排序；每轮只取最高优先级层（`#collectTier`） |
| 冲突即停并列证据 | 同层多条规则对同一路径给出不同替换值 → `termination: "conflict"`，证据含路径、当前值、各规则提案、状态哈希（`#detectConflict`） |
| 振荡识别（A: x→y，B: y→x） | 每步对状态做规范化哈希（对象键排序、数组保序），哈希重复即 `termination: "oscillation"`，证据列出构成环的 (step, ruleId, path) |
| 每步可逆修改 | 日志 `journal` 记录 `{step, ruleId, path, before, after}`，逆序回放可精确还原 |
| 不动点或预算终止 | 无可应用规则 → `fixpoint`；应用步数达 `maxSteps` → `budget` |
| 回滚恢复输入而非规范化副本 | 输入构造时深拷贝并冻结，规范化序列化仅用于哈希；`rollback()` 先逆放日志做完整性校验，再返回原始输入的深拷贝（键序原样保留） |
| 错误定位到具体路径 | 所有诊断（`skipped`、`conflict`、`oscillation`、日志）均携带 RFC 6901 JSON Pointer 路径（`src/pointer.ts`） |
| 对象键无序、数组有序 | `canonicalStringify` 对对象键排序、数组保持顺序（`src/json.ts`） |

## 运行样例（`npm run sample` 摘要）

1. **fixpoint**：`coerce-bool`(10) → `trim-name`(5) → `rename-x-to-y`(1) 按优先级
   依次应用 4 步后无规则可匹配，终止于不动点。
2. **oscillation**：`A:x-to-y` 与 `B:y-to-x` 同优先级作用于 `/mode`，
   第 2 步状态哈希与初始重复，引擎停止并给出环
   `[A@/mode, B@/mode]`；`rollback()` 还原为 `{"mode":"x"}`。
3. **conflict**：`set-theme-dark` 与 `set-theme-blue` 同优先级改写 `/theme`，
   0 步即停，证据列出双方提案与当前值 `"light"`。
4. **budget**：`increment-counter` 永不满足，3 步预算耗尽终止；
   `rollback()` 还原 `{"counter":0,"keep":[1,2,3]}`。

## 测试结果

`node --experimental-strip-types --test test/engine.test.ts`（Node v23.9.0）：
**13 个测试全部通过**（pass 13 / fail 0），覆盖：不动点、优先级顺序、
前置条件拒绝执行、缺失路径定位、A↔B 振荡识别、同层冲突证据、
高优先级胜出、预算终止、回滚逐字节还原输入（含键序）、日志逆放一致性、
对象键无序/数组有序语义、JSON Pointer 转义。
