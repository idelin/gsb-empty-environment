# sample-schema-inferrer

从 JSON 样本**归纳** schema 候选集。所有结论都是对保留样本的归纳推断，
不是对未观察数据的普遍保证。

## 运行环境

- Node.js >= 22.6（利用原生 TypeScript 类型擦除，零依赖、零构建；见 `.nvmrc`）
- 测试使用 Node 内置测试运行器（`node:test`），不引入任何第三方验证器

```sh
nvm use            # 或任何 >= 22.6 的 Node
npm test           # node --test
npm run demo       # 示例：推断 + 撤回异常样本
node src/cli.ts [--retract <id>]... [--compact] <file.json>...
```

## 设计原则（与需求逐条对应）

- **候选集而非单一结论**：输出两个候选 —— `observed-fit`（贴合样本的最紧
  schema）与 `generalized`（去掉一切非证据强制的归纳假设）。按样本支持度
  （并列时按简约度 = 归纳假设数）排序。
- **证据计数与不确定项**：`evidence` 中每个字段有 present/missing/null
  分别计数及来源样本 id；`uncertainties` 列出每条归纳跳跃及其告诫。
- **样本未见字段 ≠ 禁止**：永不输出 `additionalProperties:false`；未见属性
  标记为 unknown（`unseen-properties` 不确定项）。
- **只见整数不保证无小数**：产生 `integer-vs-number` 轴 —— strict 候选用
  `integer`，generalized 用 `number`，并标注 sample-inductive。
- **缺失与 null 分别统计**：`missing`（键不存在）与 `null`（值为 null）
  独立计数、独立来源。观察到 null ⇒ 两个候选都可空（证据强制）；观察到
  缺失 ⇒ 两个候选都可选（证据强制）。
- **来源计数与撤回收缩**：证据始终从当前保留样本集重算；撤回异常样本后
  范围随之收缩（见下方演示），`provenance.retracted` 保留撤回记录。
- **独立自验证**：每个候选都对全部保留样本运行手写验证器，
  `selfCheck.allRetainedSamplesAccepted` 必须为 true。
- **空样本返回未知**：`status: "unknown"`，不给任何猜测。
- **只承诺实现的子集**：`type`（含联合）、`properties`、`required`、
  `items`、`minimum/maximum`、`minLength/maxLength`、`minItems/maxItems`、
  `enum`。其余一律报 unknown。
- **错误双路径**：验证错误同时携带 `instancePath`（如 `$.user.age`）与
  `rulePath`（如 `#/properties/user/properties/age/type`）。
- **区分候选的最小测试样例**：`distinguishers` 为每个分歧轴生成一个最小
  JSON 实例，并用自有验证器核实“被 generalized 接受、被 observed-fit
  拒绝”（`verified: true`）。

## 运行样例（摘录）

`sh examples/run-demo.sh`，5 个用户样本（含 age=121 的异常样本 user-5）：

```jsonc
// 候选 1 generalized（简约度最高：0 条归纳假设）
{ "type": "object", "properties": { "age": { "type": "number" }, ... } }

// 候选 2 observed-fit（17 条归纳假设，全部标注）
{ "type": "object",
  "properties": { "age": { "type": "integer", "minimum": 29, "maximum": 121 } },
  "required": ["active", "age", "name"] }

// 区分样例（已经验证：generalized 接受 / observed-fit 拒绝）
{ "axis": "integer-vs-number", "path": "$.age",
  "instance": { "active": false, "age": 29.5, "name": "Ada" },
  "rejectedBy": [{ "profile": "observed-fit",
    "error": { "instancePath": "$.age",
               "rulePath": "#/properties/age/type",
               "message": "expected type integer, got number" } }],
  "verified": true }

// 撤回异常样本 user-5 后：范围收缩，来源保留
"provenance": { "retained": ["user-1","user-2","user-3","user-4"],
                "retracted": ["user-5"] }
"age": { "type": "integer", "minimum": 29, "maximum": 54 }   // 121 -> 54
```

完整输出见 `npm run demo`。

## 测试结果

```
$ node --test 'tests/*.test.ts'
ℹ tests 33
ℹ pass 33
ℹ fail 0
```

覆盖：空样本→unknown、整数/小数轴、缺失 vs null 分离计数、撤回收缩范围、
混合类型联合、自验证、验证器双路径错误、区分样例逐条核实、enum 候选阈值、
未见字段不禁止、嵌套对象与数组合并证据。

## 已知边界（有意为之）

- 候选集限定为两个剖面（strict / generalized），不做全组合爆炸。
- 数组元素证据合并到 `path[]`；对象内的计数是实例级（数组内对象可重复计数）。
- 字符串去重超过 50 个不同值后停止跟踪（不报 enum 候选）。
