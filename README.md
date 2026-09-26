# versioned-doc-loader

按文档版本字段选择 schema、沿**显式迁移链**迁移并**再验证**的文档载入器。
TypeScript 编写，零第三方依赖；验证器为手写实现，不借用任何现成验证库；
测试使用 Node.js 内置 `node:test`。

## 运行环境

- Node.js ≥ 23.6（原生 TypeScript type-stripping，源码仅使用可擦除语法）
- Node 22.6+ 可加 `--experimental-strip-types` 运行
- 可选：`npm run typecheck`（需要本机已装 `typescript`，离线环境可跳过）

```bash
npm test     # node --test test/*.test.ts
npm run demo # node examples/demo.ts
```

## Schema 子集承诺

本库**只承诺**实现以下关键字，未列出的一律不解析、不生效：

`type`（含 `integer` 与类型数组）、`properties`、`required`、
`additionalProperties`（`false` 或子 schema）、`items`、`minItems`、`maxItems`、
`enum`、`const`、`minimum`、`maximum`、`exclusiveMinimum`、`exclusiveMaximum`、
`minLength`、`maxLength`、`pattern`。

## 载入管线与分阶段错误

`registry.load(doc, options)` 依次经过三个阶段，错误按阶段分开报告：

| 阶段 | 触发条件 | 结果形态 |
| --- | --- | --- |
| `version` | 文档非对象 / 缺版本字段且未显式给 `defaultVersion` / 版本未知 / 目标版本未知 | `{ ok:false, stage:"version", errors: string[] }` |
| `migration` | 无显式迁移链 / 迁移函数抛错 / 迁移产物未通过该步目标 schema | `{ ok:false, stage:"migration", errors: MigrationError[], originalDocument }` |
| `validation` | 最终文档不合目标版本 schema | `{ ok:false, stage:"validation", errors: ValidationError[] }` |

每条 `ValidationError` 同时携带**实例路径**与**规则路径**（JSON Pointer）：

```json
{ "instancePath": "/email", "schemaPath": "/properties/email/pattern",
  "keyword": "pattern", "message": "must match /^[^@]+@[^@]+$/" }
```

## 关键语义

- **缺版本不默认最新**：缺版本字段一律拒绝，除非调用方显式传 `defaultVersion`。
- **未知版本拒绝**：只接受已注册版本，报错列出受支持版本清单。
- **显式迁移链**：迁移只沿 `registerMigration(from, to, fn)` 注册的边做 BFS 寻路，找不到链即拒绝，绝不隐式猜测。
- **每步迁移后即验证**：迁移产物必须过该步目标版本的 schema，失败计入 `migration` 阶段并附完整验证错误。
- **迁移失败保留原文**：迁移在深拷贝上执行；失败时入参对象零改动，原文随结果 `originalDocument` 返回。
- **同版本号不同摘要冲突**：注册时计算规范化 JSON 的 sha256 摘要；同版本同内容幂等通过，不同内容抛 `SchemaConflictError`。

## 运行样例（`npm run demo` 实测输出节选）

```
register v1  digest=79e49db28368…
register v2  digest=3a2370f37524…
register v3  digest=0afae69db8ab…

### v1 文档迁移到 v3
{ "ok": true,
  "document": { "version": "v3", "firstName": "Ada", "lastName": "Lovelace",
                "email": "unknown@example.com" },
  "sourceVersion": "v1", "targetVersion": "v3",
  "appliedMigrations": ["v1->v2", "v2->v3"] }

### 未知版本 v9
{ "ok": false, "stage": "version",
  "errors": ["unknown version \"v9\"; supported: v1, v2, v3"] }

### 缺版本（无默认）
{ "ok": false, "stage": "version",
  "errors": ["missing version field \"version\"; refusing to assume latest — pass defaultVersion explicitly to opt in"] }

### v3 数据不合规
{ "ok": false, "stage": "validation",
  "errors": [
    { "instancePath": "/firstName", "schemaPath": "/properties/firstName/minLength", ... },
    { "instancePath": "/email",     "schemaPath": "/properties/email/pattern",     ... },
    { "instancePath": "/age",       "schemaPath": "/properties/age/minimum",       ... } ] }

### 迁移抛错（原文保留）
{ "ok": false, "stage": "migration",
  "errors": [{ "step": "v1->v2", "message": "migration threw: disk read failed" }],
  "originalDocument": { "version": "v1", "fullname": "Ada Lovelace" } }
原对象未被改动: {"version":"v1","fullname":"Ada Lovelace"}

### 同版本号不同摘要
SchemaConflictError: schema conflict for version "v1": existing digest 79e49db28368… != incoming 819a6d2755e1…
```

## 测试结果（Node v23.9.0，`npm test` 实测）

```
✔ 同版本号不同内容 → 摘要冲突
✔ 未知版本拒绝
✔ 缺版本默认拒绝；显式 defaultVersion 才放行
✔ v1 文档沿显式迁移链 v1→v2→v3 并再验证
✔ 迁移抛错：分阶段报告且原文保留
✔ 迁移产物不合目标 schema：报 migration 阶段并带验证细节
✔ 无迁移链可达目标版本：拒绝而非隐式猜测
✔ 当前版本数据不合自身 schema：validation 阶段，错误含双路径
✔ targetVersion 指定后只迁移到该版本
✔ 合法实例通过
✔ required 缺失：给出实例路径与规则路径
✔ 嵌套属性：instancePath 与 schemaPath 同步深入
✔ 数组元素错误定位到具体下标
✔ additionalProperties=false 报告多余字段
✔ 数值边界与枚举
✔ 一次收集多个错误而非遇错即停
✔ type 数组与 integer 判定
ℹ tests 17  ℹ pass 17  ℹ fail 0
```

## 目录结构

- `src/schema.ts` — schema 子集类型、规范化序列化、sha256 摘要
- `src/validator.ts` — 手写验证器，错误含 `instancePath` / `schemaPath`
- `src/registry.ts` — 版本注册、摘要冲突检测、显式迁移链、三阶段载入管线
- `examples/demo.ts` — 六种场景的端到端样例
- `test/` — `node:test` 测试（验证器 8 例 + 注册表管线 9 例）
