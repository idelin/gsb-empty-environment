# Wasm 导入能力校验

在 `WebAssembly.instantiate()` 之前校验插件模块的宿主导入。校验器只读取 Type、Import、Function 和 Start 段；其他段的长度会被检查，但不会遍历其内容。

## 使用

```ts
import { validateWasmImports, type CapabilityTable } from "./index.ts";

const capabilities: CapabilityTable = [
  {
    module: "env",
    field: "log",
    params: ["i32"],
    results: [],
  },
  {
    module: "env",
    field: "memory",
    kind: "memory",
    min: 1,
    max: 16,
  },
];

const result = validateWasmImports(wasmBytes, capabilities);
if (!result.ok) {
  throw new Error(result.reason ?? "Wasm imports are not authorized");
}

await WebAssembly.instantiate(wasmBytes, hostImports);
```

函数能力必须同时声明 `module`、`field`、`params` 和 `results`。参数与结果按顺序逐项比较，因此 multi-value 结果不会被截断或只比较首项。`name` 或 `fieldName` 可作为 `field` 的兼容别名。

memory、table、global 导入必须使用匹配的 `kind: "memory" | "table" | "global"` 能力条目显式授权。条目中的 `min`、`max`、`shared`、`memory64`、`elementType`、`table64`、`valueType`、`mutable` 是可选约束；声明后会精确匹配。

如果 Start 段直接引用导入函数，该函数能力还必须设置 `allowStart: true`。start 指向模块内函数时，会校验其类型为 `() -> ()`。

返回值包含：

- `ok` / `valid`：是否允许继续实例化。
- `errors`：结构化错误，含稳定的 `code`、`message`、导入位置信息。
- `reason`：所有拒绝原因拼接后的字符串，成功时为 `null`。
- `imports` / `start`：解析出的导入和 start 授权结果。

常用拒绝码：`IMPORT_NOT_AUTHORIZED`、`IMPORT_KIND_MISMATCH`、`IMPORT_SIGNATURE_MISMATCH`、`START_NOT_AUTHORIZED`、`START_SIGNATURE_INVALID`。

## 测试

```sh
npm test
```

测试覆盖合法模块、multi-value 结果错配、别名导入、未授权 start，以及 memory/table/global 的显式授权。
