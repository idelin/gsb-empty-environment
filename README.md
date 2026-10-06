# path-router

TypeScript 路径路由库：字面段、命名段、末尾多段、可选组；确定性的优先级匹配；
编译期报告同优先级重叠（含见证路径），并附路径枚举核对。

## 模式语法

```
/users/list            字面段
/users/:id             命名段：恰好匹配一段，解码后存入 params.id
/files/*path           末尾多段（splat）：匹配剩余 1+ 段，只能在最后一整段
/users(/:id)           可选组：整段取舍，可嵌套，如 /a(/b(/:c))
/a(/b)/c               可选组可出现在模式中部
```

约束：splat 不允许出现在可选组内；同一模式内参数名不得重复；可选组不可为空。
模式按解码后的段进行匹配，字面量请写解码后的形式（如 `/a b` 可命中 `/a%20b`）。

## 匹配优先级

对同一条路径，逐路由取最优展开后比较：

1. **字面段更多**者优先；
2. **可选组省略更少**者优先；
3. **注册序号更小**者优先。

## 百分号解码

先按 `/` 分段，再逐段 `decodeURIComponent`（非法编码保留原文）。
因此 `%2F` 解码得到的 `/` **不会重新分段**：`/x%2Fy` 仍是一段，
`/:one` 命中且 `params.one === "x/y"`，而 `/:a/:b` 不会命中。

## 编译期冲突检测

`compile()` 时对路由两两做展开合一（unification）。若两条路由在某条路径上
最优得分（字面数、省略数）相同、且没有第三条路由在该路径上严格更优，
则胜者取决于注册顺序，属于二义性，会以**见证路径**形式报告到
`compiled.conflicts`（或 `onConflict: "throw"` 时抛 `ConflictError`）：

```ts
import { compileRouter } from "path-router";

const router = compileRouter([
  { pattern: "/users/:id", handler: showUser },
  { pattern: "/users/new", handler: newUser },   // 字面更多，无冲突
  { pattern: "/users/:name", handler: byName },  // 与 #0 同优先级重叠
]);
// router.conflicts[0].witness === "/users/<v0>"
```

`crossCheckConflicts(router)` 提供独立的路径枚举核对：枚举短路径验证
“已报告的见证在运行期确实并列”且“枚举范围内没有未报告的并列”，
测试中对固定表与 300 张随机路由表（maxLength=4）核对通过。

## API

- `createRouter<T>()` / `new Router<T>()`：`add(pattern, handler)` 链式注册，`compile()` 编译。
- `compileRouter(entries, { maxExpansions?, onConflict? })`：一次性编译。
- `compiled.match(path)`：最优命中 `{ handler, pattern, index, params, score }` 或 `null`。
- `compiled.matchAll(path)`：全部命中，按优先级排序。
- `compiled.conflicts`：`{ firstIndex, secondIndex, firstPattern, secondPattern, witness, score }[]`。
- `parsePattern` / `expandPattern`：模式解析与可选组展开。
- `enumeratePaths` / `crossCheckConflicts`：路径枚举核对。

## 测试

需要 Node ≥ 22.6（`--experimental-strip-types`，23.6+ 默认开启）：

```sh
node --test "test/*.test.ts"
```
