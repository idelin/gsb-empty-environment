# field-group-contract

字段组契约校验器(TypeScript,零依赖,不借用任何现成验证器)。只承诺下列已实现的
schema 子集,子集之外的结构一律以 `SchemaError` 拒绝并给出规则路径。

## 运行

```sh
npm test        # Node.js 内置测试运行器(node --test)
npm run sample  # 运行样例 samples/run-sample.ts
```

要求 Node >= 22.6(直接执行 TypeScript 类型擦除;在 23.6+ 上无需任何标志)。

## 已实现的 schema 子集(契约边界)

```ts
{ groups: GroupRule[] }

GroupRule =
  | { kind: 'exactlyOne',          fields: string[], when?: Condition, id?: string }
  | { kind: 'atLeastOne',          fields: string[], when?: Condition, id?: string }
  | { kind: 'atMostK', k: number,  fields: string[], when?: Condition, id?: string }

Condition =
  | { op: 'present' | 'absent', field }
  | { op: 'equals' | 'notEquals', field, value }        // 深比较
  | { op: 'and', all: Condition[] }
  | { op: 'or',  any: Condition[] }
  | { op: 'not', cond: Condition }
```

字段是实例(普通对象)的顶层键。除此之外的任何 kind / op / 结构都会被拒绝。

## 存在性语义(核心契约)

- 字段**存在** = 实例对象的**自有属性**(`hasOwnProperty`),与值无关。
- `false`、`null`、`0`、`''`、自有 `undefined` 都算存在;存在 != 真值,存在 != 非 null。
- 因此 `{a: false, b: null}` 对 `exactlyOne{a,b}` 是**冲突**(两个都存在),不是"缺失"。

## 条件启用与未决策略

条件是三值的:`true` / `false` / `undecided`。引用缺失字段的 `equals`/`notEquals`
为未决(`present`/`absent` 总是可决)。未决时按契约策略 `onUndecided` 处理:

- `'inactive'`(默认):该组视为未启用;
- `'active'`:该组视为启用;
- `'error'`:产生 `undecided` 类型的 issue(同时携带实例路径与规则路径),该组视为未启用。

补字段只承诺"存在性",不承诺值;依赖新增字段之值的条件保持未决。

## 联合可满足性(交叠组 + 条件启用)

- 所有条件启用的组**联合**求解;单组各自可行绝不当作联合可行。
- 求解器(`src/solver.ts`)把三种组统一编码为基数约束 `min <= sum <= max`,
  做边界一致性传播;传播对交叠基数约束不完备,故 `check()` 回退到
  **小域枚举**并逐一验证每个见证(默认字段域上限 20,超出拒绝)。
- 求解器支持 `push()` / `pop()`:局部假设、临时约束、临时变量全部记录在
  trail 上,`pop()` 精确恢复传播状态。最小冲突核的删除式极小化即建立在此之上。
- 补字段候选会**重新求值所有条件**:新增字段可能启用或关闭条件组
  (条件不假设单调,例如补 `c` 可以通过关闭 `when: absent(c)` 的组来生效)。

## 输出

`validate(instance, schema, options)` 返回:

- `status`: `'valid' | 'missing' | 'conflict'`;`valid` 还要求无任何 issue。
- `issues[]`: 每条都同时携带 `instancePath`(实例内 JSON Pointer,根为 `''`)
  和 `rulePath`(schema 内 JSON Pointer,如 `/groups/0`、`/groups/1/when`)。
- `completions`: **最小补字段集合**——最小基数的全部见证,每个都经完整重算验证。
- `conflictCores`: **子集最小冲突核**(删除式极小化),以原始字段条件表达:
  `facts` 为参与冲突的实例字段(存在性事实),`rules` 为参与的规则路径。
  与冲突无关的事实和规则不会出现在核中。

## 复杂度

小域枚举对字段数是指数的(`maxFields` 默认 20)。冲突核极小化对
"事实 + 规则"集合做线性次可满足性查询,每次查询本身是小域枚举。
