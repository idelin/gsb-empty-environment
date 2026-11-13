# 混合内容活动请求

https页面上的脚本、XHR和ws仍可能落到http。安全文档发起的脚本、获取、模块和WebSocket必须是https或wss；图片升级失败则当失败，不回退明文。安全文档请求https资源放行。请求组装调用可导入的TypeScript库做这次裁决。在安全文档上请求http脚本和ws。

## 策略（Mixed Content）

- 非安全文档（http/ws 页面）不做混合内容拦截。
- 安全文档请求 `https` / `wss` / `data:` / `blob:` 资源直接放行。
- 活动内容 `script`、`module`、`xhr`、`fetch`、`websocket` 落到 `http`/`ws` 一律拦截，不做自动升级。
- 被动内容（`image` 等）落到 `http`/`ws` 时升级为 `https`/`wss`；图片升级后再探测一次，探测失败即判定为 `upgrade-failed`，绝不回退明文 URL。

## 用法

```ts
import { decideMixedContent, assembleRequest } from "./index.ts";

// 同步裁决：安全文档上的 http 脚本与 ws 被拦截
decideMixedContent({
  documentUrl: "https://shop.example/checkout",
  resourceUrl: "http://cdn.example/app.js",
  kind: "script",
}); // { allowed: false, reason: "active-mixed-content", ... }

decideMixedContent({
  documentUrl: "https://shop.example/checkout",
  resourceUrl: "ws://socket.example/stream",
  kind: "websocket",
}); // { allowed: false, reason: "active-mixed-content", ... }

// 安全资源放行
decideMixedContent({
  documentUrl: "https://shop.example/checkout",
  resourceUrl: "https://cdn.example/app.js",
  kind: "script",
}); // { allowed: true, url: URL { https://... }, upgraded: false }

// 图片：升级到 https 并探测，失败时不回退 http
await assembleRequest({
  documentUrl: "https://shop.example/checkout",
  resourceUrl: "http://img.example/cat.png",
  kind: "image",
  probe: (url) => fetch(url, { method: "HEAD" }).then(() => true).catch(() => false),
});
// 探测成功 -> { allowed: true, url: https://img.example/cat.png, upgraded: true }
// 探测失败 -> { allowed: false, reason: "upgrade-failed",
//               originalUrl: "http://...", upgradedUrl: https://... }
```

## API

- `decideMixedContent(request): MixedContentDecision` — 纯函数、同步裁决，请求组装阶段调用。
- `assembleRequest(options): Promise<MixedContentDecision>` — 在裁决之上处理图片升级探测；可用 `probe` 注入自定义探测（默认走全局 `fetch` 的 HEAD 请求）。
- `isBlocked(decision)` — 类型守卫，判断是否被拦截。
- 辅助函数：`resolveUrl`、`upgradeToSecure`、`isSecureScheme`、`isPlaintextScheme`、`inferSecureContext`。

`MixedContentRequest` 字段：`documentUrl`（相对 URL 的解析基址，省略时视为安全上下文）、`resourceUrl`、`kind`、可选的 `isSecureContext`。

## 测试

```sh
npm test
```

基于 `node:test` 与 Node 原生 TypeScript 类型擦除，覆盖活动内容拦截、安全资源放行、图片升级成功/失败不回退、非安全文档与非法 URL 等场景。
