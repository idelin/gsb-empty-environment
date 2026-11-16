# Client Hints 委托与降级

无依赖的 TypeScript Client Hints 预请求附加包。它记录 HTTPS 顶层导航响应中的
`Accept-CH` 与 `Permissions-Policy`，并在发出请求前只返回当前文档允许携带的
`Sec-CH-*` 头。

```ts
import { ClientHints } from "./index.ts";

const clientHints = new ClientHints({
  hints: {
    "Sec-CH-UA": '"Chromium";v="131"',
    "Sec-CH-UA-Mobile": "?0",
    "Sec-CH-UA-Platform": '"macOS"',
    "Sec-CH-UA-Model": '"MacBookPro18,3"',
  },
});

clientHints.observeResponse({
  url: "https://app.example/",
  isTopLevelNavigation: true,
  headers: {
    "Accept-CH": "Sec-CH-UA-Model",
    "Permissions-Policy": 'ch-ua-model=(self "https://widget.example")',
  },
});

const headers = clientHints.getRequestHeaders({
  url: "https://widget.example/embed",
  topLevelOrigin: "https://app.example",
});
// { "Sec-CH-UA-Model": '"MacBookPro18,3"' }
```

规则：

- `Sec-CH-UA`、`Sec-CH-UA-Mobile`、`Sec-CH-UA-Platform` 默认只发同源请求。
- 其余提示视为高熵提示：同源请求仍需顶层 `Accept-CH` 声明；跨源请求还必须由
  对应的 `ch-ua-*` Permissions-Policy 特性明确包含目标来源，`*` 表示任意来源。
- 只有 HTTPS 顶层导航响应可以更新声明。HTTP 顶层导航会清空该来源的旧声明，
  子资源响应不会改变当前文档。
- `getRequestHeaders()` / `headersForRequest()` 返回可直接合并到请求头中的普通对象，
  不修改原请求。`attachClientHints(instance, request)` 是便捷包装。

运行测试：`npm test`。
