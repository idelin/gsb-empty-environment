# 无头浏览器渲染的请求拦截判定包

文档与截图服务用无头浏览器渲染用户提供的页面，攻击者可能在页面里放
`file:///etc/passwd` iframe、用 meta refresh 跳向云元数据地址
`169.254.169.254`，或用 `fetch` 访问内网。本包输出可直接传给
Playwright/Puppeteer 请求拦截回调的判定逻辑，默认失败关闭（fail-closed）。

## 拦截规则

- **协议白名单**：只允许 `http` 与 `https`；`file:`、`chrome:`、
  `view-source:`、`ftp:` 及其他一切协议拒绝。
- **data URI**：仅当资源类型与 MIME 同时为图片（`image/*`）或字体
  （`font/*`）时放行，`data:text/html` 等一律拒绝。
- **blob URI**：只允许发起页面同源、由本页创建的 blob；跨源或无发起者拒绝。
- **WebSocket / EventSource**：资源类型为 `websocket`、`eventsource` 一律拒绝。
- **主机公网判定**：主机名交给调用方回调解析，解析出的**全部**地址都必须是
  公网地址；任一地址为内网/保留地址即拒绝。回调抛错或返回空数组同样拒绝。
- **IP 字面量按字面判定**：内置严格解析器，不依赖 URL/Socket 的自动规范化，
  覆盖 `0x7f.1`（十六进制）、`0177.0.0.1`（八进制）、`2130706433`
  （单段十进制整数）等宽松写法，以及 IPv6（含 `::ffff:` 内嵌 IPv4）。
  私网、环回、链路本地、CGNAT、组播、保留与文档段均判非公网。
- **重定向**：重定向链的每一跳都重新执行完整策略，重定向链加当前请求的
  总跳数不超过 5，超限拒绝；拒绝结果带触发问题的具体跳 URL。
- **主页面白名单**：主页面（无发起 frame 的导航请求）主机必须在白名单内，
  支持精确主机名与 `.example.com` 形式的子域通配；子资源可以是任意公网主机。

## 用法

```ts
import { createRequestPolicy } from "./index.ts";

const policy = createRequestPolicy({
  // 调用方提供的 DNS 解析回调，例如 dns/promises 的 lookup all
  resolveAddresses: async (hostname) => {
    const records = await dns.promises.lookup(hostname, { all: true });
    return records.map((record) => record.address);
  },
  allowedHosts: ["docs.example.com", ".trusted.example.org"],
  maxRedirects: 5,
});

const decision = await policy.decide({
  url: "https://cdn.example.net/logo.png",
  type: "image",
  initiatorFrameUrl: "https://docs.example.com/render",
  redirectChain: [],
});
// decision: { allowed: true } 或 { allowed: false, reason, url, detail? }
```

### Playwright

```ts
import { playwrightRouteGuard } from "./index.ts";

const guard = playwrightRouteGuard(policy);
await page.route("**", async (route) => {
  if (await guard(route.request)) {
    await route.continue();
  } else {
    await route.abort();
  }
});
```

### Puppeteer

```ts
import { puppeteerRequestHandler } from "./index.ts";

const isAllowed = puppeteerRequestHandler(policy);
await page.setRequestInterception(true);
page.on("request", async (request) => {
  if (await isAllowed(request)) {
    await request.continue();
  } else {
    await request.abort();
  }
});
```

适配层只依赖 Playwright/Puppeteer 的结构形状，不 import 浏览器框架本身。

## 拒绝原因

`reason` 取值：`websocket-denied`、`eventsource-denied`、`invalid-url`、
`scheme-denied`、`data-type-denied`、`blob-cross-origin`、
`hostname-not-allowed`、`private-address`、`resolve-failed`、
`too-many-redirects`。

## 结构与测试

- `src/ip.ts`：宽松 IP 字面量解析与公网判定。
- `src/policy.ts`：协议门、白名单、解析回调与逐跳重定向策略。
- `src/adapters.ts`：Playwright/Puppeteer 资源类型与请求结构适配。
- `test/`：54 个用例，覆盖 file iframe、重定向到 `169.254.169.254`、
  十进制 IP、`data:text/html`、合法 CDN 图片及各类边界。

运行测试（零依赖，使用 Node 内置测试运行器与类型擦除）：

```sh
npm test
```
