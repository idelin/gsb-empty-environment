# safe-path-query

受限属性路径与集合过滤查询引擎（TypeScript，零依赖，Node.js 内置测试）。

## 安全模型

- **只读显式数据**：只允许标量、普通对象（`Object.prototype` 或 null 原型）与数组。
  类实例、函数、`Date`、`Map` 等宿主对象一经触及即报 `HOST_OBJECT`，绝不调用。
- **原型链不可见**：属性只通过 `Object.getOwnPropertyDescriptor` 读取，
  继承属性（`hasOwnProperty`、`toString` 等）永远不可见。
- **不触发 getter / 代理回调**：访问器属性视为不存在，从不执行 `[[Get]]`/`[[Set]]`/`[[Call]]`。
  （调用方不得传入 Proxy——其描述符陷阱属于宿主行为，策略上禁止。）
- **禁用键**：`__proto__`、`constructor`、`prototype` 在解析期即拒绝（`FORBIDDEN_PROPERTY`），
  枚举时亦跳过，即使它们是自有属性。
- **错误即用户路径**：所有错误为 `PathError`，携带 `code` 与 `userPath`，
  消息只引用用户表达式与键名，不含内部对象。
- **解析期深度限制**：段数 ≤ 32、过滤器嵌套 ≤ 8、表达式 ≤ 512 字符、集合 ≤ 64 项，
  超限在解析期报 `DEPTH_LIMIT`。
- **过滤策略**：仅整数比较、字符串/布尔/null 相等、有限集合 `in`、`&&`/`||`/`!`，
  无算术、无函数调用、无外部身份服务。

## 执行模型

- 路径表达式**编译为执行计划**（`compilePath` → `Plan`），由 `PlanCursor` 以显式工作栈执行。
- **可暂停**：`cursor.run(maxSteps)` 每若干微步让出，可恢复；`takeMatches()` 增量取结果。
- **有界预算**：递归下降、过滤求值（按编译期静态成本）、输出发射共同消耗同一预算，
  耗尽抛 `BUDGET_EXCEEDED`。
- **数组洞 vs 显式 undefined**：洞（hole）视为缺失（跳过/无匹配）；
  显式 `undefined` 是存在的值，可正常匹配。
- **结构版本缓存**：`DocumentStore` 的每次 `set`/`remove` 递增版本号；
  缓存条目记录求值时读取的具体路径（路径证据），局部变更仅使证据与变更路径重叠的
  条目失效，无关查询继续命中。
- **独立慢解释器**：`evaluateSlow` 以纯递归直接解释 AST（独立的过滤器实现），
  测试中与编译计划在随机深树、数组洞、恶意属性名上交叉验证。

## 路径语言

```
a.b[0]['c d'].*..e                       键 / 索引 / 字符串键 / 通配 / 递归下降
items[?(@.price >= 10 && @.tag in {'a','b'} || !(@.n == 0))]
```

## 运行

需要 Node ≥ 22.6（利用内置 TypeScript 类型擦除；开发机验证版本：v23.9.0）。

```sh
npm test        # node --test test/*.test.ts
npm run sample  # node sample/demo.ts
npm run typecheck  # 需本地安装 typescript（tsc --noEmit）
```

## 文件

- `src/parser.ts` — 词法/语法、深度限制、禁用键（解析期）
- `src/safeobj.ts` — 安全属性访问层（自有数据描述符、原型不可见、getter 不触发）
- `src/plan.ts` — AST → 执行计划（含过滤器静态成本）
- `src/vm.ts` — 可暂停预算 VM（`PlanCursor`）
- `src/interpreter.ts` — 独立慢解释器（对照实现）
- `src/store.ts` — 版本化存储 + 证据驱动的缓存失效
- `test/` — 解析、安全、数组洞、预算、缓存、交叉验证（node:test）
- `sample/demo.ts` — 运行样例
