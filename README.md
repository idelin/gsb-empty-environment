文档通过 Reporting-Endpoints 声明报告端点，攻击者利用响应头注入、重定向或DNS重绑定让浏览器向内网发送 POST。为TypeScript浏览器网络栈写可导入的报告投递包（Reporting API 与 RFC 8941 字典）：多个同名头按字典规则合并且同键重复参数按规范处理，成员值须为字符串，解析失败整头忽略；相对 URL 以响应 URL 解析且须为 HTTPS 且不含用户信息。投递前校验目标及所有重定向跳，主机解析出的任一 IP 为回环、私网或链路本地地址（含 IPv4 映射的 IPv6、十进制与十六进制整数写法）时，除非文档自身来自同一地址空间，一律拦截；请求不带 Cookie 与 Authorization，Content-Type 为 application/reports+json，单次数组不超过 100 条，超限按来源隔离丢弃而非全局清空。测重定向跳转内网、多IP重绑定、整数IP、字典冲突及凭据隔离。

## 用法

```ts
import { ReportDeliveryService } from "./src/index.ts";

const service = new ReportDeliveryService({
  documentUrl: response.url,          // 文档自身 URL，用于地址空间判定
  resolver: (host) => dnsLookup(host), // 注入 DNS 解析，返回全部 A/AAAA 记录
  transport: (req) => send(req),       // 注入实际网络发送（不得附加凭据）
});

// 从响应头配置端点（多个同名字段值直接传入数组）
service.setEndpoints(response.headers.getAll("Reporting-Endpoints"), response.url);

// 按来源 origin 入队，flush 时投递
service.enqueue(reportOrigin, "default", reports);
const results = await service.flush();
```

## 模块

- `src/structured-dict.ts` — RFC 8941 字典解析器；同键/同名参数取首次出现，语法错误返回 `null`（整头忽略）。
- `src/ip-address.ts` — IP 字面量解析（WHATWG 形式：十进制/十六进制/八进制整数、IPv4 映射/兼容 IPv6）与地址空间分类（`public` / `private` / `loopback` / `link-local`）。
- `src/reporting-endpoints.ts` — `Reporting-Endpoints` 头 → 端点组 URL 映射；仅接受字符串成员、HTTPS、无用户信息，相对 URL 以响应 URL 为基准。
- `src/report-delivery.ts` — 按来源隔离的队列与投递；每次 POST 前校验目标及每个重定向跳（协议、用户信息、解析出的全部 IP 的地址空间），超限部分按队列丢弃。

## 安全规则

- 目标主机解析出的**任一** IP 为回环/私网/链路本地时拦截，除非文档自身明确处于同一地址空间。
- 每个重定向跳重新解析并校验，防御 DNS 重绑定与跳转到内网。
- 请求仅携带 `Content-Type: application/reports+json`，不带 Cookie 与 Authorization。
- 单次投递数组最多 100 条，超限按来源 origin 隔离丢弃。

注意：校验时的 DNS 解析与传输层实际连接之间存在 TOCTOU 窗口，完整防御需在传输层将连接固定到已校验的 IP（connect-to-IP）。

## 测试

```sh
node --experimental-strip-types --test
```
