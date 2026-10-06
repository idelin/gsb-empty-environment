# ts-unify

一个 TypeScript 类型合一（unification）库，支持类型变量、函数类型、列表类型与等式约束求解。

## 类型表示（src/types.ts）

- `TVar`：类型变量（按 `id` 区分，显示为 `a`、`b`、…、`z`、`aa`）
- `TCon`：基础类型常量（`Int` / `Bool` / `Str`）
- `TFun`：函数类型 `(param -> result)`
- `TList`：列表类型 `List<element>`

## 替换（src/subst.ts）

- `Subst = Map<number, Type>`，以三角形式存储（值域中可含仍被绑定的变量）
- `applySubst`：**传递应用**替换——沿变量链一路解析，并递归进入复合类型内部，直到不动点，而不是只替一层
- `normalizeSubst`：把每个值域完全解析，得到幂等替换
- `composeSubst(outer, inner)`：先应用 `inner` 再应用 `outer` 的合成

## 合一（src/unify.ts）

`unify(constraints)` 接受 `{ left, right }` 等式约束数组，返回：

- `{ ok: true, subst }`：`subst` 已规范化（值域完全解析）
- `{ ok: false, error }`：
  - `occurs` 错误：occurs check 失败。`error.chain` 是**导致递归的一条替换链**：
    首元素是本次尝试的绑定 `v := t`，后续是沿当前替换展开 `t` 时经过的已有绑定，
    链尾的值域中再次出现 `v`。`error.message` 会打印该链。
  - `mismatch` 错误：刚性类型冲突（如 `Int` vs `Bool`、列表 vs 函数），报告完全解析后的双方。

occurs check 通过 `findPathToVar` 在遵循现有替换的情况下查找目标变量，
因此能检测 `a := List<b>`、`b := (a -> Int)` 这类间接递归，并输出完整链条。

## 测试（test/run.mjs）

- 基本合一、函数/列表分解、显示格式
- 替换传递性：变量链 `a=b=c=List<Int->Bool>` 全链解析；`composeSubst` 合成
- 不可满足样例：直接/间接 occurs（链长 1、2、3）、刚性冲突、形状冲突、深层冲突，
  并验证错误链确实闭合回环（把链上替换传递应用回尝试绑定后目标变量再次出现）
- 可满足性质测试（300 个种子轮次）：随机生成无环替换 `S`，
  用 `S` 生成约束（含 `(v, S(v))` 与 `(t, S(t))`），合一必须成功；
  随后**把解代回**每条约束验证两边相等，并验证解的每个值域已完全解析

## 运行

```sh
npm test   # node scripts/build.mjs && node test/run.mjs
```

说明：当前环境无网络、无法安装 `typescript`，因此 `scripts/build.mjs`
用一个小型类型擦除器把 `src/*.ts` 转成 `dist/*.js` 后直接在 Node 上运行测试。
源码仅使用可擦除语法；`tsconfig.json` 已配置好，装有 TypeScript 的环境可直接 `tsc --noEmit` 做类型检查。
