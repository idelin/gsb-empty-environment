# schema-compat

TypeScript 模式库的兼容性判定：给定旧模式与新模式，判断**旧模式的所有合法记录是否都能被新模式接受**；若不兼容，返回一个**最小的具体反例记录**。

## 模式语言

```ts
import { t } from "./src/index.ts";

const oldSchema = t.object({
  id:   t.req(t.number()),                          // 必填字段
  role: t.req(t.enum(["admin", "user", "guest"])),  // 有限枚举域
  nick: t.opt(t.nullable(t.string())),              // 可选 + 可空（两个正交概念）
  tags: t.opt(t.array(t.string(), { minLen: 1, maxLen: 4 })), // 数组长度界
});
```

- `required` 是**字段级**属性（记录是否必须携带该键）；`nullable` 是**值级**属性（值能否为 `null`），两者正交。
- `enum` 声明有限值域（`string | number`），是兼容性判定的主要有限域来源。
- `array` 支持 `minLen` / `maxLen` 长度界，缺省为 `[0, ∞)`。

## 判定规则

`checkCompatibility(oldSchema, newSchema)` 递归比较两个模式，失配点包括：

| 失配 | 最小反例 |
| --- | --- |
| 枚举收窄 | 被删除的首个枚举值 |
| `string`/`number` 收窄为 `enum` | 不在枚举内的最短代表值 |
| 可空被移除 | `null` |
| **新增必填字段（不可忽略）** | 缺省该字段的最小旧记录 |
| 可选变必填 | 缺省该字段的最小旧记录 |
| 数组 `minLen` 抬高 | 旧 `minLen` 长度的短数组 |
| 数组 `maxLen` 压低 | `newMax + 1` 长度的长数组 |
| 嵌套对象/数组元素失配 | 装入子反例的最小外层记录 |

多个失配点并存时，按节点数返回规模最小的反例，并附带 JSON 路径（如 `$.meta.tags[]`）与原因。

结构性假设：对象默认**开放**（旧记录多出的字段被新模式忽略）；传 `{ allowExtraFields: false }` 可切换为封闭对象，此时旧模式多声明的字段也会产生反例。

## 用法

```ts
import { checkCompatibility } from "./src/index.ts";

const result = checkCompatibility(oldSchema, newSchema);
if (!result.compatible) {
  console.log(result.path, result.reason);
  console.log(JSON.stringify(result.witness)); // 最小反例：旧模式接受、新模式拒绝
}
```

## 有限域枚举核对

`enumerateValues(schema)` 按规模递增枚举模式的代表值（无限类型按小字母表与长度窗口截断为有穷集合）。测试中的 `crossCheck` 用它对每个用例做穷举交叉核对：

1. 判定为兼容 ⇔ 枚举域内不存在被新模式拒绝的记录；
2. 判定为不兼容 ⇔ 返回的 witness 被旧模式接受、被新模式拒绝，且规模等于枚举域内最小反例。

## 运行测试

源码为可擦除语法的 TypeScript，依赖 Node 原生类型擦除（Node ≥ 22.18，无需编译步骤）：

```sh
npm test        # 等价于 node --test test/compat.test.ts
```

如安装了 TypeScript，可用 `npx tsc --noEmit` 做类型检查（配置见 `tsconfig.json`）。
