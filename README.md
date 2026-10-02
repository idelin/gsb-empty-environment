# rec-schemata

一个零依赖的递归 schema 验证器（TypeScript 实现，仅用 Node.js 内置模块与内置测试运行器，不借任何现成验证器）。

## 承诺的 schema 子集（仅这些）

- 对象：`{ type: 'object', properties?, required? }`
- 数组：`{ type: 'array', items }`
- 联合：`{ oneOf: [...] }`（必须恰好匹配一个分支）
- 显式引用：`{ $ref: '#...' }`（仅本地 JSON-pointer 引用，含 `'#'` 自引用）
- 标量叶子：`{ type: 'string' | 'number' | 'boolean' | 'null' }`

子集之外的构造（如 `allOf`、`integer`、非本地引用）在编译期直接拒绝。
实例必须是有限树（JSON 值）。

## 设计要点

- **共享验证图**（`src/compiler.ts`）：每个 schema 路径只编译一次，递归引用编译为指向同一节点的回边；悬空引用编译期拒绝；纯 `$ref` 链环（无消耗引用环）与仅经 `oneOf` 边的环在编译期以"非 productive 递归"拒绝，穿过对象属性/数组元素的递归环才是 productive，允许存在。
- **图验证器**（`src/validator.ts`）：以 `(节点id@实例路径)` 为访问状态防止无限展开；实例深度上限与错误数上限耗尽时返回 `incomplete`，绝不当作 schema 不通过；每个 `oneOf` 分支获得独立的访问状态副本，分支间共享图节点但不串用验证状态；错误同时携带 `instancePath` 与 `schemaPath`，去重键为 `(instancePath, schemaPath, message)`，同实例路径不同规则路径不会误去重。
- **独立解释器**（`src/interpreter.ts`）：不依赖编译产物，直接在原始 schema 文档上解释执行，带步数预算；预算耗尽返回 `incomplete`。用于对小有界实例交叉核验图验证器（`crossCheck`）。
- **增量实例修改**（`src/store.ts`）：`InstanceStore.set/remove` 只重验证受影响区域（路径链上最外层 `oneOf` 或修改点容器），随后在一次提交中原子地增撤错误并更新依赖索引（`errorsAt(instancePath)`）。

## 运行

需要 Node.js >= 22.6（利用内置 TypeScript 类型擦除直接运行 `.ts`，无需编译步骤）：

```sh
node --test test/*.test.ts   # 或 npm test
node examples/demo.ts        # 或 npm run demo
```

## 运行样例（`node examples/demo.ts` 节选）

```
=== compiled graph ===
  nodeCount: 6, "children.items points back to root": true

=== invalid instance (errors with instancePath + schemaPath) ===
  /size            #/$defs/node/properties/size        no oneOf branch matched (2 branches tried)
  /children/0      #/$defs/node                        missing required property "name"

=== depth budget exhausted -> incomplete ===
  verdict: "incomplete", reason: "instance depth limit exceeded (maxDepth=8) ..."

=== incremental: after fixing /children/0/name ===
  verdict: "valid", errors: []

=== cross-check: graph validator vs independent interpreter ===
  good: true, bad: true, starvedBudget: "incomplete"

=== dangling ref ===            rejected: dangling reference "#/$defs/nope"
=== non-productive ref cycle === rejected: non-productive reference cycle at #/$defs/a ...
=== non-consuming oneOf cycle === rejected: non-consuming reference cycle through oneOf branches ...
```

## 测试结果

`node --test test/*.test.ts`（Node.js v25.5.0）：**26 个测试全部通过**，覆盖：

- 编译器：共享图节点、悬空引用拒绝、纯引用环拒绝、oneOf 无消耗环拒绝、productive 递归放行、子集外构造拒绝
- 验证器：递归树验证与终止、错误双路径、oneOf 恰一分支语义、分支状态隔离、深度/错误上限返回 incomplete、去重键含规则路径
- 解释器：与图验证器在小有界实例上结论一致、预算耗尽返回 incomplete、自递归 schema 有限树可终止
- 增量存储：错误增撤、依赖索引更新、数组位移重验证、oneOf 祖先分支翻转、根替换；每次增量修改后都与全量重验证结果比对一致
