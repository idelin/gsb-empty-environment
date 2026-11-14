# 单一路径模型网关规范化组件

网关按 `/api/tenant/` 前缀授权，而后端会忽略 `;` 后面的矩阵参数。如果鉴权路径和路由路径各自做字符串处理，`/api/tenant;admin/secret` 这类目标就会让两个组件看到不同的资源。本仓库提供一个可嵌入 TypeScript 网关的规范化组件：请求目标只解析一次，鉴权和路由消费同一个 `NormalizedPath` 对象。

## 路径模型

- 百分号编码只解码一次；`%2F`、`%5C` 成为路径分隔符，`%3B` 成为真正的矩阵参数分隔符。
- 重复斜杠和空段折叠为一个分隔符；非根路径的尾随斜杠单独记录。
- `.`、`..` 在解码后解析，不能借编码绕过前缀边界。
- 路径段名称统一大小写并去掉尾随点，避免后端文件系统语义造成第二个路径模型。
- 矩阵参数保留在 `path` 和 `segments[*].matrix` 中，绝不静默丢弃；后端实际使用的 `resourcePath` 同时记录在同一个对象里。
- 如果 `;` 出现在鉴权前缀的某个路径段上（例如 `tenant;admin`），该段不再等于 `tenant`，请求被拒绝。
- 所有规范化失败、越权和不存在的路由都返回同一个 `403 Forbidden` 响应。

## 使用

```ts
import {
  createGateway,
  normalizeRequestTarget,
} from "./index.ts";

const gateway = createGateway({
  policy: { allowedPrefixes: ["/api/tenant/"] },
  routes: [
    {
      path: "/api/tenant/secret",
      resource: "tenant:secret",
      methods: ["GET"],
    },
  ],
});

const normalized = normalizeRequestTarget("/api/tenant/secret;v=1");
// normalized.path         === "/api/tenant/secret;v=1"
// normalized.resourcePath === "/api/tenant/secret"

const response = gateway.handle({
  method: "GET",
  target: "/api/tenant/secret;v=1",
});
// response.allowed === true
// response.normalized.resourcePath === response.route.path

gateway.handle({ method: "GET", target: "/api/tenant;admin/secret" });
// 403, body "Forbidden"，与其它拒绝响应完全一致
```

`authorizeRequest`、`authorizeNormalizedPath`、`matchRoute`、`handleRequest` 和 `PathNormalizer` 均从 `index.ts` 导出。测试命令：

```sh
npm test
```
