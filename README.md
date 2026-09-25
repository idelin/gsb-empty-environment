# policy-containment

判断一个策略的适用范围是否包含另一个策略的适用范围。条件语言受限且安全：
策略只读**显式声明的属性**，仅支持有限集合谓词、整数条件（含两个整数变量的
差分约束）与布尔组合 —— 不访问任何外部身份服务。

## 条件语言

| 构造 | 含义 |
| --- | --- |
| `in(attr, [...])` | 有限枚举成员 |
| `cmp(attr, op, k)` | 整数比较（`< <= == != >= >`） |
| `diff(a, b, op, k)` | 差分约束 `a - b <op> k` |
| `setHas / setSubset / setIntersects` | 集合谓词 |
| `and / or / not / true_ / false_` | 布尔组合 |

域是显式有限的：`enum`（值列表）、`int`（整数区间，`min > max` 为空范围）、
`set`（允许的集合列表）。属性声明 `optional: true` 表示可能未知。

## 判定方法

- **共享决策表示**：所有条件经 `ConditionFactory` 哈希一致性（hash-consing）
  共享规范节点；盒求值按 `(节点, 盒)` 记忆化，共享子条件不重复求值。
- **区间分割**：在盒（区间/值列表的笛卡尔积）上用区间算术求三值（T/F/M）
  近似，确定为假的盒整体剪枝，混合盒按最大维度二分 —— 不对整数值逐一枚举。
- **最小反例**：存在性搜索找到反例后，按属性序逐属性二分最小化，返回字典序
  最小反例。
- **三值语义**：未知属性按 Kleene 强三值逻辑求值；只要条件引用了可能未知的
  属性，包含关系即返回 `unknown`（无法证明）。
- **预算中断**：盒求值步数超过 `budget` 返回 `undecided`。
- **独立穷举检查器**（`src/exhaustive.ts`）：对小域逐赋值枚举，独立验证包含
  判定、交/并/差集合运算律与反例真实性。

判定结果：

```ts
type ContainsResult =
  | { status: "contained" }
  | { status: "not-contained"; counterexample: Assignment }
  | { status: "undecided"; reason: "budget"; stepsUsed: number }
  | { status: "unknown"; reason: "unknown-attributes"; attributes: string[] };
```

## 运行

需要 Node.js >= 22.18（内置 TypeScript 类型擦除与 `node:test`），无第三方依赖。

```sh
npm test    # node --test test/*.test.ts
npm run demo
```

## 运行样例（`npm run demo` 输出节选）

```
== x>=3 contains x>=2 ?
   verdict: not-contained
   minimal counterexample: {"x":2}
   counterexample verified by exhaustive checker: true

== empty range contained in anything        (x in [5,3]，空范围)
   verdict: contained

== unknown attribute domain                 (x optional)
   verdict: unknown
   cannot prove: attributes may be unknown -> x

== difference constraint: x-y<=3 contains x-y<=5 ?
   verdict: not-contained
   minimal counterexample: {"x":4,"y":0}

== set predicate: perms has 'read' contains perms subsetOf {read}?
   verdict: not-contained
   minimal counterexample: {"perms":[]}

== tiny budget -> undecided                 (x in [0,1000000], budget=2)
   verdict: undecided
   budget exhausted after 2 steps
```

## 测试结果

`node --test test/*.test.ts`：**30 个测试全部通过**，包括：

- 区间/枚举/集合/差分约束的包含判定与最小反例（`x>=3` 不包含 `x>=2`，反例 `x=2`）
- 空范围被任何范围包含（空 int、空 enum、空 set、不可满足条件）
- 未知属性域返回 `unknown`，穷举检查器一致
- 预算中断返回 `undecided`，加大预算后正常判定
- 10^12 量级空间用区间分割在少量步数内判定（不逐值枚举）
- 200 组随机条件：符号引擎与独立穷举检查器判定、反例及其最小性完全一致
- 布尔组合与满足集的交/并/差运算律交叉验证

```
# tests 30
# pass 30
# fail 0
```
