# RN 批量桥调用分发器

可导入的 TypeScript 批量调用组件，用于校验并分发原生模块的
`[moduleIds[], methodIds[], params[]]` 调用。

## 使用

```ts
import { BatchDispatcher, type CallbackHandle } from "./index.ts";

const dispatcher = new BatchDispatcher({
  callbackSink: (callbackId, args) => {
    // 将回调转发给原生宿主
  },
  logger: console,
});

dispatcher.registerModule(7, {
  0: {
    // 方法签名：字符串 + 映射（值必须是数组）
    params: ["string", { type: "map", values: { type: "array" } }],
    handler: (params) => params[0],
  },
  1: {
    params: ["callback"],
    handler: (params, context) => {
      const callback = params[0] as CallbackHandle;
      callback("done");       // 第一次触发
      callback("replayed");   // 记录 CALLBACK_REPLAY，不再转发
      return context.nativeParams[0]; // 签发的整数 callback id
    },
  },
});

const result = dispatcher.dispatchBatch(
  [7, 7],
  [0, 1],
  [["value", { items: [1, 2] }], [null]],
);
```

`dispatchBatch` 返回批次及逐调用结果：

- `accepted: false` 表示三个数组本身不合法（非数组或长度不等），此时整批不执行。
- `results[i].ok` 表示单个调用是否成功；一个调用失败不会阻断后续调用。
- `errors` 包含批次或单调用校验/执行错误。
- `callbackEvents` 记录每次回调尝试，`securityEvents` 记录所有拒绝与重放事件。

需要让批次信封错误直接抛出时使用 `dispatchBatchStrict`（或
`assertBatchEnvelope`）；单调用失败仍保留在结果中。

## 校验规则

- `moduleIds`、`methodIds`、`params` 必须等长，否则整批拒绝。
- 模块和方法 ID 必须是非负安全整数，且已完成注册。
- 参数个数必须与方法签名一致；支持 `string`、`number`、`boolean`、
  `array`、`map`、`callback`，以及嵌套的 `items` / `values` / `keys` 规格。
- 数组和映射的嵌套深度硬上限为 8（可通过 `maxDepth` 调低，不能调高）。
- 拒绝循环引用、非有限数字、非普通对象映射和未知值类型。
- 每个回调获得唯一的非负整数 ID。通过句柄或 `invokeCallback(id, ...)` 只
  能消费一次；再次尝试返回 `CALLBACK_REPLAY` 并写入安全事件。
- 同一外部回调函数或数字回调 token 在后续调用中重复出现时，
  该调用返回 `CALLBACK_TOKEN_REPLAY`，后续调用继续执行。

事件可以通过 `getCallbackEvents()`、`getSecurityEvents()` 获取，也可通过
`logger.warn` 和 `onSecurityEvent` 实时观察。

## 测试

```sh
npm test
```

测试覆盖不等长数组、负数/越界 ID、参数类型与深度超限、回调重放以及
单调用失败隔离。
