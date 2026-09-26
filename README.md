# joint-presence-constraints

零依赖 TypeScript 库：基于**字段存在性**的字段组约束校验 + 联合可满足性求解。
不借用任何现成验证器；校验、求解、最小冲突核、最小补字段集合均为本仓库实现。
运行环境：Node.js ≥ 23.6（原生 TypeScript 类型擦除），测试使用 Node 内置 `node:test`。

## 承诺的 schema 子集（仅以下结构受支持，其余一律 `SchemaError` 拒绝）

```ts
interface Schema {
  groups?: Group[];                    // 顶层字段组，全部针对同一实例联合检查
  onUndecidedCondition?: 'disabled' | 'enabled' | 'error'; // 未决契约策略，默认 'disabled'
}

interface Group {
  kind: 'exactlyOne' | 'atLeastOne' | 'atMost';
  fields: string[];                    // 非空，顶层字段名
  k?: number;                          // 仅 atMost，整数 >= 0
  when?: Condition;                    // 条件启用
  name?: string;
}

type Condition =
  | { op: 'present'; field: string }
  | { op: 'absent'; field: string }
  | { op: 'equals'; field: string; value: Json }
  | { op: 'and'; all: Condition[] }
  | { op: 'or'; any: Condition[] }
  | { op: 'not'; cond: Condition };
```

不承诺：嵌套字段路径、类型/格式校验、`dependencies` 等其它关键字。

## 语义契约

- **存在 ≠ 真值**：字段存在 = 对象自身拥有该键（`hasOwnProperty`）。`false`、`null`、`0`、`''` 都算存在；只有键缺失才算不存在。
- **条件三值求值**：`present`/`absent` 总可判定；`equals` 引用的字段缺失时条件为**未决**（undecided），按 `onUndecidedCondition` 契约策略处理：
  - `disabled`（默认）：该组不启用；
  - `enabled`：按启用处理；
  - `error`：产出 `keyword: 'condition'` 错误。
- **错误双路径**：每个错误同时携带 `instancePath`（JSON 指针，组级错误为 `''`，未决条件错误指向缺失字段如 `/mode`）与 `rulePath`（如 `/groups/2`、`/groups/0/when`）。
- **联合检查**：所有（被条件启用的）组对同一实例同时检查；重叠组不会各自为政。

## 求解器

- **小域**：每个字段的域为 `{ABSENT, OTHER} ∪ {equals 字面量}`，`OTHER` 表示“非任何字面量的其它值”，对该条件语言是完备抽象。
- **联合可满足性**：`new Solver(schema).solve()` 在所有启用约束上联合搜索，单组各自可行不代表联合可行。
- **见证枚举**：`solver.enumerate()` 枚举小域内全部见证；测试中与暴力全乘积交叉验证数量一致。
- **push/pop**：`push()` / `assume(field, value)` / `pop()` 以 trail 方式局部修改并精确恢复传播状态；基数约束做边界传播（如 atLeastOne 只剩一个开放字段时强制其在场）。
- **最小冲突核**：`minimalUnsatCore(schema)` 用删除式极小化，输出原始规则路径（`/groups/i`）的子集最小冲突核。
- **最小补字段集合**：`minimalCorrections(schema, instance)` 按改动字段数递增枚举，输出最小修复（`set` 补/改字段、`unset` 删字段），可同时包含“缺失补全”和“冲突删除”。

## API 一览

```ts
import {
  validate, assertSchema,            // 校验
  Solver, jointSatisfiable, findWitness, ABSENT, OTHER, // 求解
  minimalUnsatCore, minimalCorrections, materialize,     // 冲突核 / 修复
} from './src/index.ts';
```

## 运行

```sh
npm test      # node --test test/*.test.ts
npm run sample
```

## 测试结果

`node --test`：25 个测试全部通过（presence / validate / solver / core 四组），覆盖：
存在与真值区分、三种组约束、条件启用与三种未决策略、错误双路径、
单组可行但联合不可行、小域枚举与暴力对照、push/pop 状态恢复、
子集最小冲突核、最小补字段集合（含增删组合与条件停用修复）。
