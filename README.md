# pairwise-path-router

TypeScript 路径路由库：支持字面段、命名段、末尾多段通配与可选组，
匹配优先级为「更多字面段 → 更少可选省略 → 注册序号」，
并能在路由表编译期报告同优先级重叠（附见证路径，经路径枚举核对）。

纯 TypeScript（仅可擦除类型语法），零依赖，Node ≥ 22.18 可直接运行。

## 模式语法

| 语法 | 含义 | 示例 | 匹配 |
| --- | --- | --- | --- |
| `/users/list` | 字面段 | `/users/list` | 仅自身 |
| `/users/:id` | 命名段（单段） | `/users/42` | `params.id = "42"` |
| `/files/*path` | 末尾多段通配（零或多段，仅允许位于模式末尾） | `/files/a/b` | `params.path = ["a", "b"]` |
| `/users(/:id)` | 可选组（整段省略或保留，可嵌套） | `/users`、`/users/7` | 省略时无 `id` |

约束：模式须以 `/` 或 `(` 开头；通配符必须是顶层末段；同名参数不可重复；
空段（`//`、末尾 `/`）非法。非法模式抛出 `PatternError`。

## 匹配优先级

同一路径命中多条路由时，依次比较：

1. **字面段更多者优先**（按实际命中的展开变体计）；
2. **可选组省略更少者优先**；
3. **注册序号小者优先**。

一条路由含可选组时先展开为若干变体，路由内取最优变体，再跨路由比较。

## 百分号解码

先按 `/` 切分原始路径，再对每段 `decodeURIComponent`。
因此 `%2F` 解码出的斜杠**不会**重新分段：

```ts
import { Router } from "./src/index.ts";

const router = new Router([{ path: "/files/:name", data: "one" }]);
router.match("/files/a%2Fb")?.params; // { name: "a/b" } —— 仍是一个段
```

畸形百分号序列（如 `/%`）视为不匹配。

## 冲突检测（编译期）

`router.conflicts()` 对路由两两做段序列合一，生成候选见证路径，
再用真实匹配器复核双方最优优先级，相等即报告：

```ts
import { Router } from "./src/index.ts";

const router = new Router([{ path: "/a/:id" }, { path: "/a/:name" }]);
router.conflicts();
// [{ routes: [0, 1], priority: { literalCount: 1, omittedCount: 0 }, witness: "/a/x", ... }]

Router.compile([{ path: "/a/:id" }, { path: "/a/:name" }]); // 抛出 ConflictError
```

`enumerateConflicts(routes, maxDepth)` 提供有界路径枚举（字母表 =
模式中出现的字面量 + 新鲜符号），逐路径计算各路由最优优先级以发现并列，
用于核对合一法的结果——测试中以数百组随机模式交叉验证两者一致。

## API 摘要

- `new Router(defs)` / `router.add(path, data)`：注册路由。
- `router.match(rawPath)`：最优匹配（含 `route`、`data`、`params`、`literalCount`、`omittedCount`），无命中返回 `null`。
- `router.matchAll(rawPath)`：全部命中，按优先级与注册序排序。
- `router.conflicts()`：同优先级重叠列表（含见证路径）。
- `Router.compile(defs)`：构建并校验，存在冲突时抛出 `ConflictError`。
- `parsePattern(source)` / `expand(pattern)`：模式解析与可选组展开。
- `decodePath(rawPath)`：切分并逐段解码。

## 测试

```sh
npm test   # node --test "test/*.test.ts"
```
