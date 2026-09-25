# json-migration-graph

JSON 文档的版本图迁移引擎（TypeScript，零运行时依赖）：

- **版本图路由**：迁移步骤是有向加权边，Dijkstra 选择唯一最小代价路径；
  两条及以上同代价路径判定为歧义并拒绝（`AMBIGUOUS_PATH`）。
- **逐步 schema 校验**：每步声明 `inputSchema` / `outputSchema`，执行前后各校验一次。
- **断点恢复**：journal 记录原始输入的规范化哈希、已完成步骤名与中间状态；
  恢复时校验原始输入一致（`CHECKPOINT_INPUT_MISMATCH`）且已完成步骤是计划
  路径前缀（`CHECKPOINT_PLAN_MISMATCH`），已完成步骤跳过不重放，
  因此追加的字段不会重复。
- **失败不发布**：`publish` 回调仅在全部步骤成功且最终校验通过后调用一次。
- **JSON 树语义**：对象键无序（规范化哈希对键排序），数组有序（索引进入路径）。
- **错误可定位**：所有错误携带具体路径（如 `$.address.zip`、`$.tags[1]`）、
  步骤名与错误码。

## 环境要求

Node.js >= 22.6（使用内置 type stripping 直接运行 `.ts`，无需编译、无第三方依赖）。
仓库内置 `.nvmrc`（`nvm use` 即可）。

## 运行

```sh
npm test        # node --experimental-strip-types --test test/*.test.ts
npm run sample  # node --experimental-strip-types examples/sample.ts
```

## 结构

- `src/json.ts` — JSON 树类型、规范化序列化/哈希、路径格式化、JSON 兼容性检查
- `src/schema.ts` — 结构 schema 与校验器，问题定位到具体路径
- `src/graph.ts` — 版本图与确定性路由（唯一最短路 / 歧义拒绝）
- `src/journal.ts` — 断点 journal（文件版原子写入 + 内存版）
- `src/engine.ts` — 迁移引擎：校验 → 路由 → 断点校验 → 逐步执行 → 成功才发布
- `src/errors.ts` — `MigrationError`（code / step / path / issues）
- `examples/sample.ts` — 六个场景的运行样例
- `test/` — node:test 测试（29 个用例）

## 错误码

| code | 含义 |
| --- | --- |
| `INVALID_JSON_TREE` | 输入不是 JSON 兼容树（NaN、函数等），路径定位 |
| `SCHEMA_INPUT` / `SCHEMA_OUTPUT` | 步骤输入/输出违反 schema，路径 + 步骤名 |
| `STEP_APPLY` | 步骤函数抛异常 |
| `NO_PATH` | 版本间无路由 |
| `AMBIGUOUS_PATH` | 存在多条同代价最优路径，拒绝并列出 |
| `CHECKPOINT_INPUT_MISMATCH` | 恢复时原始输入与 journal 哈希不一致 |
| `CHECKPOINT_PLAN_MISMATCH` | journal 中已执行步骤不是当前计划前缀 |

## 验证结果（Node v22.14.0）

- `npm test`：29 个用例全部通过（schema 路径定位、路由确定性与歧义拒绝、
  崩溃恢复不重复追加、篡改输入拒绝、失败不发布、文件 journal 跨实例恢复等）。
- `npm run sample`：演示确定性路由、歧义拒绝、schema 错误路径定位、
  崩溃→断点恢复（`history` 无重复）、篡改拒绝、仅成功才发布。
