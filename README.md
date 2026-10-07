# pair-router

组合式 TypeScript 路由构造库：父子路径拼接、参数校验，并保证
**构造出的 URL 一定能被反解析回同一路由、同一组参数**。

零依赖，需要 Node ≥ 23.6（原生运行 TypeScript）或 ≥ 22.6（加
`--experimental-strip-types`）。

```bash
npm test
```

## 用法

```ts
import { route } from "pair-router";

const members = route("/org/:orgId?").extend("/members/:memberId");

members.build({ orgId: "o1", memberId: "m1" }); // "/org/o1/members/m1"
members.build({ memberId: "m1" });              // "/org/members/m1"（父可选段省略）
members.match("/org/members/m1");               // { memberId: "m1" } —— 子参数来源独立
```

## 路径模式

| 写法       | 含义                         |
| ---------- | ---------------------------- |
| `users`    | 静态段                       |
| `:id`      | 必填参数段                   |
| `:id?`     | 可选参数段（省略时整段消失） |

## 规则

- **重复参数名**：同一有效路径上（含 `extend` 组合后）参数名必须唯一，
  构造时即抛错。
- **可选父段省略**：子段参数按自身段位置独立取值，不会被父段挤占；
  解析器对可选段按“省略优先”回溯，与 `build` 的规范化输出一致。
- **往返保证**：`build` 产出 URL 后会自检 `match(url)` 是否还原为同一组
  参数，无法还原（如可选参数值与后续静态段文本相同）则抛出歧义错误。
  连续两个可选段（`/:a?/:b?`）天然歧义，在构造时直接拒绝。
- **空串**：必填参数空串抛错；可选参数空串视为省略。
- **保留字符**：参数值逐段 `encodeURIComponent` / `decodeURIComponent`，
  值中的 `/`、`?`、`#`、`%`、空格、Unicode 均不破坏段结构；非法百分号
  序列的 URL 不匹配。
- **尾斜杠**：模式中的尾斜杠被忽略；`build` 永不产出尾斜杠（根路由为
  `/`）；`match` 允许并忽略尾部斜杠，但路径中间的空段（`//`）不匹配。

## API

- `route(pattern)` / `Route.of(pattern)` — 创建路由。
- `route.extend(childPattern)` — 拼接子路径，返回新路由（原路由不变）。
- `route.build(params)` — 构造 URL，参数非法或有歧义时抛错。
- `route.match(url)` — 反解析，命中返回参数对象，否则返回 `null`；
  忽略 query/hash。
- `route.path` / `route.paramNames` — 规范化模式串与参数名列表。
