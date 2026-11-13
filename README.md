# Client Hints委托与降级

第三方 iframe 若能拿到高熵 Client Hints，就能做设备指纹，攻击者会靠伪造的 Accept-CH 或被放宽的 Permissions-Policy 扩大发送范围。为TypeScript浏览器网络栈写供请求发出前调用的 Client Hints 附加包：顶层来源用 Accept-CH 声明想接收的提示；低熵提示（Sec-CH-UA、Sec-CH-UA-Mobile、Sec-CH-UA-Platform）默认发给同源请求；高熵提示（如 Sec-CH-UA-Full-Version-List、Sec-CH-UA-Model）只发给同源，或发给 Permissions-Policy 中对应 ch-ua-* 特性的列表明确包含目标来源的跨源请求（列表为 * 时放行任意来源）；Accept-CH 只在 HTTPS 的顶层导航响应上生效，子资源响应与 HTTP 响应里的声明忽略。用无委托的跨源请求、列表含目标来源、明文页面声明、子资源声明和 * 列表验证。

## 用法

实现位于 `src/client-hints.ts`，通过 `index.ts` 重新导出。

```ts
import { ClientHintManager } from './index.ts';

const manager = new ClientHintManager({
  // 本机可提供的提示值
  hintValues: {
    'Sec-CH-UA': '"Chromium";v="120"',
    'Sec-CH-UA-Mobile': '?0',
    'Sec-CH-UA-Platform': '"macOS"',
    'Sec-CH-UA-Full-Version-List': '"Chromium";v="120.0.6099.71"',
    'Sec-CH-UA-Model': '"MacBookPro"',
  },
  // 顶层文档的 Permissions-Policy 响应头（可传数组表示多个头）
  permissionsPolicy: 'ch-ua-model=("https://analytics.example.net")',
});

// 每个响应都喂给管理器；只有 HTTPS 顶层导航响应里的 Accept-CH 会生效
manager.observeResponse({
  url: 'https://example.com/home',
  headers: { 'Accept-CH': 'Sec-CH-UA-Model, Sec-CH-UA-Full-Version-List' },
  isTopLevelNavigation: true,
});

// 请求发出前调用，允许的提示头会被合并进 request.headers
manager.attachClientHints({
  url: 'https://analytics.example.net/collect',
  clientOrigin: 'https://example.com',
  headers: {},
});
```

## 规则

- 低熵提示（`Sec-CH-UA`、`Sec-CH-UA-Mobile`、`Sec-CH-UA-Platform`）默认只随同源 HTTPS 请求发送；跨源发送要求对应 `ch-ua-*` 特性的允许列表包含目标来源或为 `*`。
- 高熵提示必须同时满足：被当前顶层来源的 `Accept-CH` 声明，且同源或被 `Permissions-Policy` 显式委托；空允许列表 `()` 完全禁止该特性。
- `Accept-CH` 仅在 HTTPS 顶层导航响应上记录，按顶层来源隔离；子资源响应与 HTTP 响应中的声明忽略。
- 非 HTTPS 请求不携带任何 Client Hint；提示头映射规则为去掉 `sec-` 前缀并归一化，如 `Sec-CH-UA-Full-Version-List` → `ch-ua-full-version-list`。

## 测试

```sh
npm test
```

测试覆盖：无委托跨源请求、允许列表含目标来源、明文页面声明、子资源声明、`*` 列表，以及空列表、`self` 混合、多响应头并集、按来源隔离等边界。
