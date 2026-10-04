# 接口兼容检查器

在有限类型系统上判断**新接口能否替代旧接口**（结构化子类型，新 ≤ 旧，Liskov 可替换），
TypeScript 实现，零依赖。

## 类型系统

| 构造 | 表示 |
| --- | --- |
| 原始类型 | `{ kind: "primitive", name: "string" \| "number" \| "boolean" \| "null" }` |
| 记录 | `{ kind: "record", fields: { [k]: { type, optional? } } }` |
| 联合 | `{ kind: "union", options: [...] }` |
| 函数 | `{ kind: "function", params: [...], returns }` |
| 命名引用（递归） | `{ kind: "ref", name }`，定义放在 `TypeEnv` 中 |

**可缺省 ≠ 可空**：

- 可缺省字段 `optional: true` —— 键可以不存在；
- 可空 `union [T, null]`（`T.nullable(t)`）—— 键必须存在，值可为 `null`。

## 子类型规则

- **记录**：宽度（新类型可多出字段）+ 深度（字段协变）；旧接口的必填字段在新接口中必须存在且必填。
- **联合**：新联合的每个分支都须 ≤ 旧类型；非联合的新类型 ≤ 旧联合，只需匹配其中一个分支。
- **函数**：参数**逆变**（旧参数 ≤ 新参数），返回值**协变**（新返回 ≤ 旧返回）；新函数参数个数不得更多。
- **递归类型**：比较维护「成对访问状态」`(新, 旧)` 集合，重复出现的对按共归纳假设兼容，比较因此终止。

## 输出

`checkReplacement(newType, oldType, env)` 返回 `{ compatible, failures }`，每个 `failure` 含：

- `path`：不兼容路径，如 `$.user.name`、`$.onEvent(参数1·逆变).msg`、`$.list.next<#1>.head`；
- `reason`：原因；
- `counterexample`：**最小值形状**反例 —— 属于新类型（逆变位置则为旧参数类型）但不属于旧类型的值形状；
  记录只含必填字段，联合取代价最小分支，递归处截断为 `<递归 Name …>`；函数位置用
  `fn(参数k = …) 被调用` / `fn(...) => …` 表示调用/返回层面的见证。

## 运行

```sh
bun run src/demo.ts    # 演示 + 断言自测（也可 npm run demo）
```

## 示例

```ts
import { checkReplacement, formatReport, T } from "./src/checker";

const oldApi = T.rec({ onEvent: T.fn([T.rec({ code: T.num })], T.str) });
const newApi = T.rec({ onEvent: T.fn([T.rec({ code: T.num, msg: T.str })], T.num) });

console.log(formatReport(checkReplacement(newApi, oldApi)));
// ✘ 不兼容：新接口不能替代旧接口（2 处）
//   1. 路径 $.onEvent(参数1·逆变).msg
//      原因: 值形状缺少此必填字段（键不存在）
//      反例: { onEvent: fn(参数1 = { code: 0 }) 被调用 }
//   2. 路径 $.onEvent(返回值·协变)
//      原因: 原始类型不兼容：number 不能当作 string 使用
//      反例: { onEvent: fn(...) => 0 }
```
