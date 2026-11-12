# Wasm 导入能力校验

插件市场在 `WebAssembly.instantiate` 之前调用本包，确认模块声明的导入与当前套餐授予的宿主能力完全一致。零依赖、零 WASM 引擎调用，只手工解析与能力判定相关的四个段。

## 威胁模型

- 同名不同签名：攻击者导入 `env.fetch` 但把 `(i32) -> i32` 改成 `(i32) -> (i32, i64)` 等 multi-value 形态，企图按错误的 ABI 调用宿主函数。
- 别名导入：把未授权能力挂到另一个模块名字段下（如 `attacker-alias.fetch`）。
- 重复导入：同一 `模块名.字段名` 被声明多次，制造链接歧义。
- start 函数：start 在实例化阶段自动执行，可在加载服务的业务代码运行前调用已导入的宿主函数。
- memory / table / global 导入：不声明在能力表中即视为未授权，且限制、元素类型、可变性必须精确匹配。

## 解析范围

只解释类型段（id=1）、导入段（id=2）、函数段（id=3）和 start 段（id=8）。custom 段及其余所有段按声明长度整体跳过；同时校验魔数/版本、段顺序、段唯一性、LEB128 编码与段尾字节，防止畸形二进制造成解析错位。

## 用法

```ts
import { validateWasmImports, assertWasmAuthorized } from './index.ts';
import { readFile } from 'node:fs/promises';

const capabilityTable = {
  allowStart: false,
  modules: {
    env: {
      fields: {
        fetch: { kind: 'func', type: { params: ['i32'], results: ['i32'] } },
        memory: { kind: 'memory', limits: { min: 1, max: 4, shared: false } },
        table: {
          kind: 'table',
          tableType: { elementType: 'funcref', limits: { min: 2, max: 2, shared: false } },
        },
        counter: { kind: 'global', globalType: { valueType: 'i32', mutable: true } },
      },
    },
  },
};

const bytes = new Uint8Array(await readFile('plugin.wasm'));
const result = validateWasmImports(bytes, capabilityTable);
if (!result.ok) {
  throw new Error(result.issues.map((issue) => issue.message).join('; '));
}

// 或在加载管线中直接使用会抛错的版本
const parsed = assertWasmAuthorized(bytes, capabilityTable);
await WebAssembly.instantiate(bytes, buildHostImports(parsed, capabilityTable));
```

## API

- `validateWasmImports(bytes, table): ValidationResult` — 不抛解析异常，`ok` 为假时返回全部命中的问题，便于一次性反馈。
- `assertWasmAuthorized(bytes, table): ParsedWasmModule` — 任一校验失败时抛出 `WasmParseError`，成功则返回解析结果。
- `parseWasmModule(bytes): ParsedWasmModule` — 仅解析四个目标段，畸形输入抛 `WasmParseError`。

## 授权规则

- 函数导入：模块名、字段名命中能力表，且参数与结果类型序列逐项一致（长度敏感，因此 multi-value 错配必然被发现）。
- memory 导入：`min`、`max`（含无 max）、`shared` 全部相等。
- table 导入：元素类型（`funcref` / `externref`）与 limits 全部相等。
- global 导入：值类型与可变性（`mutable`）全部相等。
- start：能力表顶层 `allowStart !== true` 时，任何带 start 段的模块都被拒绝；即使允许，start 索引越界或签名非 `() -> ()` 仍拒绝。

## 拒绝原因（`issue.code`）

| code | 含义 |
| --- | --- |
| `parse_error` | 魔数/版本、LEB128、段顺序或段长度等二进制结构错误 |
| `unauthorized_import` | 模块名/字段名不在能力表中 |
| `duplicate_import` | 同一模块名+字段名被重复导入 |
| `import_kind_mismatch` | 导入的 func/memory/table/global 种类与授权不一致 |
| `signature_mismatch` | 函数参数或结果类型（含 multi-value）不一致 |
| `memory_limits_mismatch` | memory 的 min/max/shared 不一致 |
| `table_type_mismatch` | table 元素类型或 limits 不一致 |
| `global_type_mismatch` | global 值类型或可变性不一致 |
| `unauthorized_start` | 当前套餐未授权 start 执行 |
| `invalid_start_function` | start 索引越界，或签名不是空参数空结果 |

## 测试

`npm test` 使用 Node 内置测试运行器。测试用手工字节构造器生成真实 wasm，并用宿主 `WebAssembly.instantiate` 交叉确认被拒模块本身是合法二进制，覆盖 multi-value 错配、别名/重复导入、未授权 start、memory/table/global 授权与完整合法模块。
