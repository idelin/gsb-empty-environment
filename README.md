# 组件模型资源句柄表

TypeScript 宿主的 resource 句柄表，用于组件实例化与调用边界：

- 每个槽记录资源类型、代次、own/borrow 状态和所属调用作用域。
- 所有句柄操作先校验代次，再校验资源类型和 own/borrow 种类。
- own 转移会立即退役源槽；源句柄随后使用会得到 `HANDLE_MOVED`。
- borrow 句柄绑定到 `CallScope`，`endCall`（包括异常路径的 `finally`）统一作废。
- 释放槽后下标可复用，但代次递增，旧句柄得到 `HANDLE_DROPPED` 或 `HANDLE_STALE`。
- 实例化要求组件导入接口集合是授权 world 的精确子集；多余或同名变体导入得到 `IMPORT_NOT_AUTHORIZED`。

## API

```ts
import { ResourceTable, instantiateComponent } from "./index.ts";

const table = new ResourceTable();
const file = table.createOwn("file", { path: "/data" });

// 组件调用边界：返回后 borrow 自动失效。
table.withCall((call) => {
  const borrowed = table.borrow(file, "file", call);
  return table.useBorrow(borrowed, "file");
});

// own 跨边界转移：旧句柄失效。
const guestFile = table.transferOwn(file, "file");

// 实例化前执行 world 子集检查。
const instance = instantiateComponent(
  { imports: ["wasi:cli/stdout@0.2.0"] },
  { imports: ["wasi:cli/stdout@0.2.0", "wasi:io/error@0.2.0"] },
);

instance.invoke((call) => {
  // 通过 instance.table 创建/借用资源。
});

// 异步组件调用使用 invokeAsync / withCallAsync；
// borrow 会保留到 Promise settle，再在 finally 中统一作废。
await instance.invokeAsync(async (call) => {
  // ...
});
```

`HandleError.code` 可用于区分 `HANDLE_TYPE_MISMATCH`、`HANDLE_BORROW_EXPIRED`、`HANDLE_STALE`、`HANDLE_MOVED`、`HANDLE_IN_USE` 等拒绝原因。

运行验证：

```sh
npm test
```

测试覆盖类型混淆、borrow 逃逸（含异常返回、异步返回与嵌套调用）、drop 后下标重用、own 转移、跨表重放以及多导入越权。
