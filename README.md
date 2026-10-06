# route-compose

TypeScript 路由构造库：父子路径组合、类型化参数、构造与反解析严格往返。

```ts
import { Route, Router } from "route-compose";

const org = new Route("/orgs/:orgId");
const member = org.child("/members/:memberId"); // /orgs/:orgId/members/:memberId

const url = member.build({ orgId: "acme", memberId: "42" });
// "/orgs/acme/members/42"

member.match(url); // { orgId: "acme", memberId: "42" }
```

运行测试（需要 Node ≥ 22.18，原生 type-stripping）：

```sh
npm test
```

## 模式语法

- `/users` — 静态段
- `/users/:id` — 必需参数段
- `/users/:id?` — 可选参数段
- `/` — 根路由

## 组合规则

- `parent.child(pattern)` 将父子段拼接为一条**有效路径**，并在组合后的整体上重新校验。
- **重复参数名**：同一有效路径上出现两次同名参数（无论必需/可选）时抛出
  `DuplicateParamError`；不同路由之间同名互不影响。
- **可选父段**：父级可选段省略时，子级参数仍从自己的段位独立解析，互不吞并：
  `new Route("/:tenant?").child("/users/:id")` 在 `build({ id: "7" })` 时得到
  `/users/7`，反解析结果为 `{ id: "7" }`，不含 `tenant` 键。
- **歧义拒绝**：两个可选段之间若没有静态段锚定（如 `/:a?/:b?`、组合后的
  `/:a?/:b/:c?`），无法保证反解析唯一，抛出 `AmbiguousPatternError`。

## 构造与反解析往返

`match(build(params))` 恒等于 `params`（可选参数省略时不出现在结果中）。
保证成立的前提规则：

- **空串**：参数值不允许为空字符串（会产生 `//` 破坏解析），抛出
  `EmptyParamError`；缺少必需参数抛 `MissingParamError`，传入未知参数抛
  `UnknownParamError`。
- **保留字符**：参数值经 `encodeURIComponent` 编码，`/`、`?`、`#`、`%`、空格、
  Unicode 等均安全往返；`match` 端先按 `/` 切分再 `decodeURIComponent`，
  因此值内编码后的 `/` 不会破坏路径结构。形似已编码的输入（如 `%2F`）按字面量
  处理（编码为 `%252F`）。
- **尾斜杠**：`build` 永不产生尾斜杠（根路由为 `/`）；`match` 容忍恰好一个尾
  斜杠（`/a/b/` ≡ `/a/b`），但 `//`、路径中间的空段、缺少前导 `/`、含 `?`/`#`
  或非法 `%` 转义的路径一律不匹配。

## Router

`Router` 按具体度排序匹配：静态段多者优先，其次必需参数多者、可选参数少者；
因此 `/users/new` 永远优先于 `/users/:id`，与注册顺序无关。

```ts
const router = new Router();
router.add("/users/new");
router.add("/users/:id");
router.match("/users/new")!.route.pattern; // "/users/new"
```

## 类型

`Route<P>` 以模式字符串为泛型参数，`build` 的参数类型由模式推导
（`ParamsOf<P>`）：必需参数必填、可选参数可省、未知键编译期报错。
