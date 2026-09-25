# modlink

显式命名导入/导出的模块系统：命名空间隔离、版本精确匹配、声明环/求值环分别建图、
原子整批链接、模块热替换（存活决策快照 + 失败回滚）、按完整依赖版本向量隔离的缓存，
以及一个纯函数式策略引擎（只读显式属性、有限集合、整数条件、布尔运算，不接外部身份服务）。

要求 Node.js >= 22.6（原生 TypeScript 类型擦除），无第三方依赖。

## 结构

- `src/types.ts` — 模块规格、导入/导出声明、版本向量
- `src/linker.ts` — 注册表、闭包解析、签名校验、双图环检测（最短错误来源链）、原子链接、HMR
- `src/policy.ts` — 策略引擎（`in` 有限集合 / 整数比较 / `and`/`or`/`not`）
- `test/` — Node.js 内置测试（`node:test`）
- `examples/sample.ts` — 运行样例

## 语义要点

- 导出分 `decl`（无求值副作用的声明引用，经 `declare` 立即提供）与 `value`（由 `init` 求值产生）。
- 声明环允许；求值环拒绝，错误信息输出最短来源链，如 `evaluation cycle detected: a -> b -> a`。
- 签名校验全部通过后才开始整批初始化；任一失败整批丢弃，存活集合与缓存不变。
- 缓存键为完整依赖版本向量（`vectorKey`），不同向量互不混用；旧向量缓存保留，
  热替换前捕获的旧异步属性仍返回旧值。
- 缺失导出错误定位到导入语句：`module 'a' import statement #1 (from 'b@1'): export 'nope' not found`。
- 策略规则按 `模块:规则名` 注册，两个模块的同名规则互不覆盖；默认拒绝。

## 运行

```sh
npm test      # node --test test/*.test.ts
npm run sample
```

## 运行样例输出

```
== diamond link ==
  linked vector: app@1|left@1|logger@1|right@1
  [logger@1] hello from left
  [logger@1] hello from right
== policy ==
  admin/3  -> allow
  viewer/9 -> deny
== hmr ==
  swapped: {"logger":"1","left":"1","right":"1","app":"1"} -> {"logger":"2","left":"2"}
  [logger@2] hello from left@2
  rollback after failure: module 'broken@1' failed to initialize: cannot start
  live set still: {"logger":"2","left":"2"}
```

## 测试结果（Node v23.9.0，17 通过 / 0 失败）

```
✔ init failure rolls back the whole batch: no partial version swap
✔ old async property still resolves the old value after hot replacement
✔ failed HMR validation keeps the live batch untouched
✔ diamond shared import: shared dependency initializes exactly once
✔ same-name shadowing: namespaces isolate identical export names
✔ version must match exactly
✔ declaration cycle is allowed (no evaluation side effects)
✔ evaluation cycle is rejected with the shortest error source chain
✔ shortestCycle picks the minimal chain among several cycles
✔ missing export error locates the import statement
✔ signature mismatch is reported before linking
✔ cache is isolated by the full dependency version vector
✔ finite set membership condition
✔ integer conditions and boolean algebra
✔ conditions are limited to finite sets and integers
✔ properties are explicit and read-only: unknown property is an error
✔ same-named rules in two modules never override each other
ℹ tests 17  ℹ pass 17  ℹ fail 0
```
