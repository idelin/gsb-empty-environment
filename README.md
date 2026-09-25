# 版本图迁移引擎（TypeScript，零依赖）

把一组迁移步骤组织成**有向版本图**，在图上选择**确定性路径**，逐步执行并对每一步做
**输入 / 输出 JSON Schema 校验**；每完成一步写入**断点**，崩溃后可用**同一原始输入**
恢复。实现为纯 TypeScript，仅使用 Node.js 内置能力（`node:test`、`node:crypto`、`node:fs`），
无 npm 依赖，适合空仓库直接运行。

## 需求与设计对照

| 需求 | 实现 |
| --- | --- |
| 迁移步骤组成版本图 | `src/graph.ts`：版本是节点，`MigrationEdge`（含多个有序 `MigrationStep`）是有向边 |
| 选择确定路径 | `resolvePath`：枚举简单路径，成本 = 各边 `priority` 之和，选全局最小 |
| 检测歧义 | 最小成本路径不唯一即抛 `AMBIGUOUS_PATH`（含全部候选与打平的两条路径），拒绝执行；`detectAmbiguities` 可全图预扫描 |
| 每步可验证输入输出 schema | 每个 `MigrationStep` 带 `input`/`output`，执行前后均用 `src/schema.ts` 校验 |
| 断点恢复校验原始输入与已执行步骤 | `Checkpoint` 记录 `inputHash`、`graphHash`、版本路径、逐步 `outputHash`；恢复时全部复核 |
| JSON 兼容树、对象键无序、数组有序 | `src/json.ts`：规范化序列化对对象键排序、数组保序，SHA-256 内容哈希 |
| 错误及来源定位到具体路径 | `MigrationError.details` 含 JSON Pointer `path`、`edgeId`、`causeLocation`、`location`、`issues[]` |
| 两条同优先路径产生歧义需拒绝 | 平局不做任何隐式 tie-break（不按边 id、不按发现顺序），直接拒绝且不执行任何步骤 |
| 迁移失败不发布新版本 | 任何一步失败都抛错；`Migrator` 只有全部成功才返回并清除断点；输入 Envelope 永不被修改 |
| 重放已完成步骤不能重复追加字段 | 恢复始终从 `originalEnvelope` 重放整条路径，而不是在中间态上继续；`addField` 对已存在键直接报错，`appendUnique` 幂等 |

## 目录结构

```
src/
  json.ts       规范化哈希、深拷贝、JSON Pointer
  errors.ts     MigrationError 与全部错误码（结构化 details）
  schema.ts     轻量 JSON Schema 校验器（问题带 JSON Pointer）
  graph.ts      建图校验、确定性选路、歧义检测
  ops.ts        纯函数字段操作（addField / appendUnique / renameField ...）
  migrate.ts    migrate / resume 引擎、Checkpoint、逐步校验与哈希
  store.ts      断点存储：原子文件存储（tmp+rename）与内存存储
  migrator.ts   门面：run / recover，发布规则
examples/
  shop-graph.ts  v1→v4 示例版本图（多步边 + 低成本捷径）
  sample.ts      6 个可运行场景
test/           node:test 内置测试，35 个用例
```

## 运行要求

- Node.js **v22.18+** 或 **v23+**（直接运行 TypeScript 的类型擦除；本仓库在 `v23.9.0` 下验证）。
- 无第三方依赖，不需要 `npm install`。

```bash
# 运行测试（内置 node:test）
node --test "test/**/*.test.ts"
# 或
npm test

# 运行样例
node examples/sample.ts
# 或
npm run sample
```

## 核心 API

```ts
const graph = buildGraph([
  { from: 'v1', to: 'v2', priority: 10, steps: [/* MigrationStep[] */] },
  { from: 'v2', to: 'v3', priority: 10, steps: [step1, step2] },
  { from: 'v1', to: 'v3', priority: 25, steps: [/* 快速通道 */] },
]);

const store = new FileCheckpointStore('./.checkpoints/migration.json');
const migrator = new Migrator(graph, store);

// 成功才返回新版本；失败抛 MigrationError，不发布
const result = migrator.run({ version: 'v1', data }, 'v3');

// 崩溃后：必须传与当初完全一致的原始输入；已执行步骤从原始输入重放
const recovered = migrator.recover(originalInput);
```

每个步骤的契约：

```ts
interface MigrationStep {
  name: string;
  input: Schema;                 // 执行前校验当前数据
  output: Schema;                // 执行后校验产出
  apply: (data: JsonObject, ctx) => JsonObject; // 纯函数，不修改入参
}
```

## 路径成本与歧义

- 路径成本 = 路径上各边 `priority` 的数字和，**数字越小越优先**。
- 若两条路径成本相同（“同优先”），无论边 id 或发现顺序如何，都返回
  `AMBIGUOUS_PATH`，`details.candidates` 给出全部打平路径、`tiedPaths` 给出前两条。
- 该检查发生在执行任何步骤之前，因此歧义拒绝没有副作用。

## 断点（Checkpoint）字段

```ts
{
  checkpointVersion: 1,
  graphHash,                 // 图结构指纹（边/优先级/步骤名）
  fromVersion, targetVersion,
  inputHash,                 // 原始输入 Envelope 的规范化哈希
  originalEnvelope,          // 恢复依据：永远从它重放
  versionPath: ['v1','v2','v3'],
  edgePath:    ['v1->v2','v2->v3'],
  executed: [                // 每完成一步追加一条（带输出哈希）
    { edgeId, stepName, version, outputHash }
  ],
  status: 'in-progress' | 'completed'
}
```

恢复（`resume`）依次校验：断点版本 → 图哈希 → 输入版本与输入哈希 →
`originalEnvelope` 与当前输入逐字段相等 → 版本路径 / 边路径一致 →
从原始输入重放已执行前缀并逐条核对 `outputHash`。任何不一致都抛带路径的错误。

## 运行样例输出（实测）

```
=== 1. resolvePath 与 migrate：捷径成本更低，确定性选中 ===
versionPath = [ 'v1', 'v4' ]
edgePath    = [ 'v1->v4' ]
cost        = 25
published   = v4
data        = {"version_marker":"shop","user":{"id":7,"name":"Ada","email":"user7@example.com"},"items":[{"sku":"a-1","qty":2}],"tags":["customer"],"updatedAt":"2026-09-25T00:00:00Z"}
steps       = [ 'v1->v4/add_user_email', 'v1->v4/add_tags_empty',
                'v1->v4/add_tag_customer', 'v1->v4/rename_updated_to_updatedAt' ]
checkpoint cleared after success = true

=== 2. 同优先路径歧义 => AMBIGUOUS_PATH（拒绝，不执行任何步骤） ===
code        = AMBIGUOUS_PATH
candidates  = [ [ 'a', 'c' ], [ 'a', 'b', 'c' ] ]
tiedPaths   = [ [ 'a', 'c' ], [ 'a', 'b', 'c' ] ]
message     = ambiguous migration from a to c: 2 paths share cost 5

=== 3. 迁移失败：定位到具体数据路径，不发布新版本，原始输入不被修改 ===
code        = OUTPUT_SCHEMA_VIOLATION
path        = /user/score
edgeId      = v1->v2
issues      = [ { path: '/user/score', reason: 'below minimum', expected: '0', actual: '-1' } ]
input unchanged = true
no published checkpoint = true

=== 4. 断点恢复：崩溃后用同一原始输入恢复，不重复追加字段 ===
crash       = simulated crash after step index 1
executed so far = [ 'add_user_email', 'add_tags_empty' ]
recovered version = v4
tags (no duplicates) = [ 'customer' ]
tags length  = 1
email present once = true

=== 5. 恢复校验：原始输入哈希不一致 => CHECKPOINT_INPUT_MISMATCH ===
code        = CHECKPOINT_INPUT_MISMATCH
expected/actual = <原始输入 sha256> / <被篡改输入 sha256>

=== 6. 规范化哈希：对象键序无关，数组顺序有关 ===
object key reorder equal = true
array reorder differs    = true
```

## 测试结果（实测）

```
ℹ tests 35
ℹ suites 0
ℹ pass 35
ℹ fail 0
ℹ cancelled 0
ℹ skipped 0
ℹ todo 0
ℹ duration_ms ~590
```

覆盖：规范化哈希性质、schema 路径定位、确定性选路、歧义拒绝（零步骤执行）、
建图非法定义定位、输入/输出 schema 违规、步骤异常包装、崩溃恢复不重复追加、
completed 断点幂等恢复、输入/图/路径/逐步哈希篡改的各类拒绝、原子文件断点与损坏文件处理。
