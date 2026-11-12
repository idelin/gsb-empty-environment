# tRPC 风格过程分发

一个无依赖、可导入的 TypeScript 过程路由与批量分发包，适用于 Node.js。

## 使用

```ts
import {
  createRouter,
  handleRequest,
  mutation,
  query,
} from "./index.ts";

const app = createRouter(
  {
    greeting: query(({ input }) => ({ message: `hello ${input.name}` }), {
      input: (value) => {
        if (typeof value !== "object" || value === null) {
          throw new Error("object input required");
        }
        return value as { name: string };
      },
    }),
    admin: {
      deleteUser: mutation(({ ctx, input }) => ({
        deleted: input.id,
        by: (ctx as { user: string }).user,
      }), {
        protected: true,
      }),
    },
  },
  {
    authorize: ({ ctx }) =>
      (ctx as { user?: string } | undefined)?.user === "admin",
  },
);

const single = await handleRequest(app, {
  method: "POST",
  path: "greeting",
  input: { name: "Ada" },
});

const batch = await handleRequest(app, {
  method: "POST",
  path: "greeting,admin.deleteUser,missing",
  body: JSON.stringify({
    0: { json: { name: "Ada" } },
    1: { json: { id: 7 } },
  }),
  context: { user: "guest" },
});
```

单次结果：

```ts
{ ok: true, path: "greeting", type: "query", data: ... }
{ ok: false, path: "greeting", type: "query", error: { code, message } }
```

批量结果：

```ts
{ ok: boolean, results: ProcedureResult[], error?: { code, message } }
```

`results` 与调用一一对应。单个元素失败不会中止其他元素，错误只包含该元素自身的路径与通用错误信息。

## 安全约束

- 过程存放在 `Map` 中，只接受路由定义的自有可枚举属性；继承属性、访问器属性不会被收集。
- 路径按 `.` 分段。任一段为 `__proto__`、`constructor`、`prototype` 时返回 `NOT_FOUND`，即使该名称被显式注册也不会命中。
- `GET` 只允许 `query`。`mutation` 与 `subscription` 返回 `METHOD_NOT_SUPPORTED`，且在鉴权、解析输入前就拒绝。
- 批量元素最多 10 个（可配置得更低，不能更高）。超限返回 `BATCH_TOO_LARGE`，不会执行任何元素。
- 每个批量元素独立查找、鉴权和解析。鉴权发生在输入 JSON 解析及过程输入校验之前。
- 鉴权失败固定返回 `UNAUTHORIZED` 和通用消息，不包含输入、输入校验错误或其他批量元素信息。
- `parseJsonStrict()` 拒绝任意层级、任意转义写法造成的重复 JSON 键，并把 `__proto__` 写成自有数据属性，避免原型污染。
- 过程抛出的未知异常转换为通用 `INTERNAL_SERVER_ERROR`。`RouterError` 可携带明确、安全的业务错误。

## 主要导出

- `createRouter` / `router` / `createProcedureRouter`
- `query`、`mutation`、`subscription`、`procedure()` 构建器
- `protectedProcedure`、`publicProcedure`
- `dispatch`、`dispatchBatch`、`handleRequest`、`handleTrpcRequest`、`handleFetchRequest`
- `parseJsonStrict`、`ERROR_CODES`、`MAX_BATCH_SIZE`

请求可显式使用 `calls: [{ path, input }]`，也兼容 `path: "a,b"` 加索引 body 的 tRPC 批量形式。对单个原始 `body` 字符串，会先鉴权，再严格解析为输入 JSON。

运行测试：

```sh
npm test
```
