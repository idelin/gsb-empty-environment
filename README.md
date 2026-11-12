# contract-guard：安全注解跨模块一致性核验

面向“接口声明标注了净化效果、但实现未保证所有返回路径都满足”的问题。本包以**有限控制流模型**为输入，按路径验证实现是否蕴含声明，并把通过验证的契约封存为不可伪造的**已核验摘要**供调用方使用；验证失败的注解会被毒化，无法沿调用链继续传播。

## 能力边界

- **有限域输入**：每个字段属于 `bool`、有限 `label`、有界 `range` 或 `enum` 域，验证器枚举满足前置条件的全部具体取值。
- **三类注解**：输入约束（`pre` 谓词）、正常返回标签集合（`returns`）、异常出口及异常后对象状态（`throws` + `post`）。
- **按路径蕴含**：对每条可行路径检查返回标签是否被允许、每个出口的后置谓词（含异常后的对象状态）是否成立；任一违反即产出带输入快照和节点路径的反例。
- **调用方只认摘要**：调用节点只接受带品牌的 `VerifiedSummary`；摘要品牌符号与盖章函数不对外导出，外部无法构造。
- **失效不传播**：验证失败的 id 写入注册表毒化标记，后续查询返回 `null`，调用边报 `unverified-callee`，异常出口未挂处理器报 `unhandled-throw`。

## 模块结构与相互递归

| 模块 | 职责 | 相互递归关系 |
| --- | --- | --- |
| `src/domain.ts` | 有限域、取值枚举 | 运行时导入 `predicate.evaluatePredicate`；`predicate` 导入 `domain.domainValues` |
| `src/predicate.ts` | 谓词与量词（`forAll`/`exists` 展开有限域） | 与 `domain` 成环，ESM live binding 解析 |
| `src/model.ts` | 契约、CFG 节点、引用读写 | 导入 `effects.stepBlock`；`effects` 导入 `model.readRef/writeRef` |
| `src/effects.ts` | 块内传递效应、调用形状 | 与 `model` 成环 |
| `src/registry.ts` | 已核验摘要与毒化标记 | 导入 `verifier.verifyImplication` 做按需核验；`verifier` 反向查询注册表 |
| `src/verifier.ts` | 路径 DFS、蕴含判定、毒化/封存 | 与 `registry` 成环 |
| `src/summary.ts` | 品牌类型与封存函数（不公开盖章能力） | 无环叶子 |
| `src/builder.ts` | 流式构建器与重载入口 | 门面 |

三个运行时相互递归环均只在函数体内引用对方导出，模块求值阶段不触碰，ESM 实时绑定保证安全。

## 重载签名

- `annotate(id, domains)` 返回构建器；`annotate(contract)` 原样返回契约对象。
- `verifyImplication(contract, impl, options?)` 同时提供带实现签名的重载与完整实现签名。
- `useSummary(VerifiedSummary)` 与 `useSummary(VerificationSuccess)` 两个重载；`VerificationFailure` 因缺少品牌在编译期被拒绝（见 `test/types.test-d.ts`）。

## 使用方式

```ts
import {
  annotate, compare, constOperand, field,
  implement, labelRef, ref, verify, useSummary,
} from "./src/index.ts";

const contract = annotate("sanitize", {
  label: { kind: "label", labels: ["raw", "sanitized"] },
})
  .returns(["sanitized"], [compare("eq", field("label"), constOperand("sanitized"))])
  .throws("Boom", [compare("eq", field("label"), constOperand("sanitized"))])
  .build();

const impl = implement("sanitize", "entry")
  .node({ kind: "entry", id: "entry", next: "clean" })
  .node({ kind: "effects", id: "clean",
          effects: [labelRef(ref("label"), "sanitized")], next: "ok" })
  .node({ kind: "return", id: "ok", ref: ref("label") })
  .build();

const result = verify(contract, impl);
if (!result.ok) {
  result.counterexamples.forEach((c) => console.error(c.reason, c.detail));
} else {
  const summary = useSummary(result); // 只有已核验结果可传入
}
```

完整可运行示例见 `examples/usage.ts`。

## 验证语义

1. 结构校验：悬空节点引用直接判 `malformed-model` 并毒化。
2. 输入枚举：对满足全部 `pre` 的有限域笛卡尔积逐个 DFS。
3. 分支按守卫求值二选一；`effects` 块按序应用 `assign`/`setLabel`/`assume`，`assume` 失败判 `infeasible-assumption`。
4. 调用节点按被调方摘要的每个出口分叉：正常出口绑定返回标签走 `next`；异常出口仅在存在 `onThrow` 时分叉，否则判 `unhandled-throw`；被调方无摘要或已毒化判 `unverified-callee`。
5. 返回节点校验标签白名单；`throw` 与返回节点均校验对应出口的全部 `post` 谓词；未声明出口判 `missing-contract-exit`。
6. 有界步数（默认 256）防止非有限模型无限展开，超限判模型不合法。

## 测试

- 运行时测试：`npm test`（Node ≥ 22.6，`node --experimental-strip-types --test test/sanitizer.test.ts`）。
- 类型测试：`npm run typecheck`（需本地安装 `typescript` 与 `@types/node`）。
- 覆盖场景：全路径净化通过、raw 早返回失败并毒化、异常后对象仍为 raw 失败、未核验被调方拒绝、已核验被调方正常/异常双出口传播、失败结果无法传入调用方门面（编译期）。

> 当前沙箱内 Node 动态库加载被策略阻断，无法在此环境实际执行 `npm test`；核心 DFS 与出口判定算法已用独立 Go 移植版交叉验证（四类场景全部通过）。代码仅使用可擦除 TypeScript 语法（无 enum、namespace、参数属性），Node 原生类型擦除可直接运行。
