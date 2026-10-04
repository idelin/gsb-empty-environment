# config-merge

分层配置合并库（TypeScript，零依赖，Node ≥ 22.6 可直接运行 `.ts`）。

## 来源与优先级

`defaults < file < env < cli`，高优先级层覆盖低优先级层：

```ts
import { loadConfig } from './src/index.ts';

const loaded = loadConfig({
  defaults: { server: { port: 80 } },
  files: ['config.json'],
  env: { prefix: 'APP_' },                 // APP_SERVER__PORT=8080 -> server.port
  cli: { argv: process.argv.slice(2) },    // --server.port=9090
  schema: {
    'server.port': { type: 'number' },
    'db.password': { type: 'string', secret: true },
  },
});

loaded.value;      // 合并结果（原始值）
loaded.display;    // 脱敏副本（秘密字段显示 ********）
loaded.provenance; // 溯源树
loaded.issues;     // 全部校验问题（带定位）
```

也可以分别使用 `fromDefaults` / `fromFile` / `fromEnv` / `fromCli` 构造层，
再交给 `mergeLayers(layers)` 合并。

## 列表操作

高优先级层中的普通数组直接**替换**；也可以用指令对象进行追加、替换、删除指定值：

```json
{ "tags": { "$append": ["prod"] } }
{ "tags": { "$remove": ["legacy"] } }
{ "tags": { "$replace": ["a", "b"] } }
{ "tags": { "$remove": ["x"], "$append": ["y"] } }
```

- `$remove` 按深度相等删除所有匹配元素；
- 指令可组合，顺序为 `$replace` → `$remove` → `$append`；
- env / CLI 中传入 JSON 字符串（如 `--tags='{"$append":["x"]}'`）会自动解析为指令。

## 溯源（provenance）

每个最终元素都记录来源与被覆盖链：

```ts
import { provenanceAt, explain } from './src/index.ts';

provenanceAt(loaded.provenance, 'server.port');
// { source: { kind: 'env', name: 'APP_SERVER__PORT' }, value: 9090,
//   overridden: [ { source: file…, value: 8080 }, { source: defaults…, value: 80 } ] }

explain(loaded.provenance, 'server.port');
// server.port: value=9090 from env:env(APP_*)
// server.port: overridden=8080 from file:config.json at config.json:3:5
// server.port: overridden=80 from defaults:defaults
```

## 秘密字段

schema 中标记 `secret: true` 的字段：

- `redact(value, schema)` / `loaded.display` 中显示为 `********`（子树整体脱敏）；
- 原始值仍保留在结果与溯源中，`valueChanged(provA, provB, path)` 可比较两次合并间秘密是否变化；
- `explain` 输出同样脱敏，日志不会泄露秘密。

## 校验与定位

所有问题以 `MergeIssue` 报告，携带 `path`（如 `db.password`、`tags[2]`）与来源定位：

| code | 触发 | 定位 |
| --- | --- | --- |
| `duplicate-key` | JSON 文件中出现重复键（后者生效） | 文件名 + 重复键行：列，消息含首次定义位置 |
| `null-value` | 合并结果中存在 `null`（可用 `nullable: true` 豁免） | 该值来源的文件行：列 / 变量名 / flag |
| `type-conversion` | env / CLI 字符串按 schema 转换失败 | 环境变量名或 `--flag` + `argv[i]` |
| `type-mismatch` | 最终值与 schema 声明类型不符 | 配置路径 |
| `invalid-list-op` | 列表指令用于非数组或指令值非数组 | 配置路径 + 来源 |

## 测试

```sh
npm test          # node --test test/*.test.ts
node examples/demo.ts
```
