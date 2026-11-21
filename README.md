# 契约核验包（Contract Verifier）

接口声明标注了净化效果（例如“返回时 `buffer.sanitized` 成立”），但实现未必在
**每一条**返回路径上都满足它。本包接收声明与实现的**有限控制流模型（DAG）**，
逐条路径验证“实现蕴含声明”，并且只向调用方发放带品牌（branded）的**已核验摘要**。

## 模型与注解

控制流节点定义在 `src/model.ts`，注解能力覆盖题目要求的三个维度：

- **输入约束**：`entry.input` 是前置条件，调用方必须能建立这些字面量才能调用。
- **返回标签**：`return` 节点带 `tag` 与 `requires`，同一标签的义务在声明编译时取交集。
- **异常后对象状态**：`throw` 节点带异常 `label` 与对抛出后状态的 `requires`；
  实现侧用 `invalidate` 节点显式撤销失效注解。

抽象状态是三值的（原子命题为真 / 假 / 未知），写时复制、路径间不共享
（`src/state.ts`）。`assume` 与已知事实矛盾时该路径判定为**不可行**，既不算
满足证据也不算反例；模型必须是 DAG，回边会被拒绝。

## 按路径核验

`src/verify.ts` 的 `verifyContract(declaration, implementation)`：

1. `enumeratePaths` 枚举全部结构路径（带节点级记忆化，检测回边）；
2. 每条路径从声明前置条件出发，逐节点解释 `assume / effect / invalidate / branch`；
3. 在 `return` / `throw` 出口检查当前状态是否蕴含声明义务；
4. 任一出口失败即返回 `VerificationFailure`，`summary` 恒为 `null`；
5. 全部通过才铸造 `VerifiedSummary`（品牌符号私有，外部无法伪造）。

## 调用方闸门

`src/client.ts` 的 `invoke` 只接受 `VerifiedSummary`：

- 编译期：未核验对象（原始声明、实现模型、失败结果）没有匹配的**重载签名**；
- 运行期：品牌校验失败抛 `UnverifiedContractError`；
- 三参重载要求调用方用事实证明前置条件，否则抛 `PreconditionViolationError`；
- 未知返回标签 / 异常标签抛 `UnknownExitError`。

## 异常出口与失效注解

`invalidate` 只撤销“当前仍成立”的字面量并降为未知，不会反向断言。异常出口的
摘要按所有到达该出口的可行路径取交集，因此被撤销的净化注解**不会**出现在异常
摘要里，调用方无法在 catch 处继续依赖它。见 `examples/sanitizer.ts`。

## 模块结构（含相互递归）

- `src/formula.ts` ↔ `src/state.ts` 相互递归：公式层拥有极性/蕴含语义，状态层
  拥有存储，彼此通过 ESM 实时绑定委托。
- `src/model.ts` 节点类型；`src/paths.ts` 有限路径枚举；`src/summary.ts` 品牌摘要；
  `src/verify.ts` 核验器（`verifyContract` 多重载）；`src/client.ts` 调用闸门
 （`invoke` 四重载）；`src/index.ts` 统一导出。

## 运行

```sh
npm test        # node --test，11 个用例
npm run typecheck
node --experimental-strip-types examples/sanitizer.ts
```

沙箱无网络，类型检查通过 `scripts/typecheck.mjs` 调用本机已有的 TypeScript 编译器
API；`test/node-shims.d.ts` 仅为无 `@types/node` 环境提供最小测试运行器声明。
