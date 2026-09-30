# plan-type-inference

对「字段投影 / 改名 / 计算 / 删除 / 分支 / 动态读取」计划做**静态类型推导**：
提前发现读取已删字段、不可达分支、未知函数等问题，产出步骤依赖图与可重放证书，
并用有限输入枚举审计“静态声称安全但运行缺字段”的反例。零依赖，TypeScript 源码
直接由 Node 原生类型擦除运行，测试使用 Node 内置 `node:test`。

## 运行要求

- Node.js ≥ 23.6（原生 TypeScript 类型擦除；22.6+ 可加 `--experimental-strip-types`）

```bash
node --test test/          # 运行全部测试
node examples/run-sample.ts # 运行样例
```

## 能力一览

- **字段存在性与类型推导**：`project / rename / compute / delete` 逐步推导（`src/infer.ts`）。
- **分支合流**：联合类型 + 字段级可缺失性；`has/eq/not/and/or` 路径敏感条件收缩；
  收缩为空 ⇒ 不可达分支警告（`narrow`，`src/infer.ts`）。
- **动态字段读取保守建模**：键未知 ⇒ 依赖全部字段，结果为全部字段类型的联合（`src/expr.ts`）。
- **未知函数不推断为 any**：报错并按 `unknown` 保守传播（`src/expr.ts` 的 `KNOWN_FNS`）。
- **步骤依赖图**：def-use 边，含 `$input` 源（`src/infer.ts`）。
- **冗余消除保持错误触发顺序**：只删可证明无错误的无操作/死步骤，绝不重排
  （`src/optimize.ts`），并用有限输入枚举验证首错一致。
- **可重放证书**：每步推导记录（读写、输入哈希、输出模式、诊断）可序列化、可重放比对
  （`src/certificate.ts`）。
- **有限枚举反例检查**：按输入模式有界枚举，逐步核对静态声称（`src/enumerate.ts`）。
- **增量失效**：缓存键 = 步骤内容 + 依赖字段切片；改一步只失效相关推导
  （`src/incremental.ts`）。
- **JSON 语义**：对象键无序、数组有序；所有哈希/比较遵循该语义（`src/json.ts`）。
- **错误定位**：诊断与运行错误均携带步骤 id 与具体路径（如 `$.x`）。

## 关键语义示例

- 先 `delete x` 后 `compute w = $.x + 1` ⇒ 编译失败，错误定位 `$.x`（步骤 `s2`）。
- 分支只一侧产生字段 `z` ⇒ 合流后 `z?: ...` 可缺失，下游读取产生警告。
- `unknown` 不是 `any`：参与算术只产生警告并继续保守传播，绝不静默通过。
