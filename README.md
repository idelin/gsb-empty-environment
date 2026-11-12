# MTA-STS 策略缓存与 MX 通配

可导入的 TypeScript MTA-STS（RFC 8461）策略缓存与投递判定组件，防御策略伪造与 `_mta-sts` DNS 记录剥离导致的明文降级。

## 安全语义

- **仅 HTTPS 取策略**：只从 `https://mta-sts.<domain>/.well-known/mta-sts.txt` 拉取；非 HTTPS URL、非 2xx 响应或解析失败一律拒绝，且不会覆盖已有缓存。默认 fetcher 禁用重定向（`redirect: "error"`）。
- **仅在 id 变化时更新**：TXT 记录缺失或 id 与缓存一致时不发起 HTTPS 请求；id 变化才重新拉取并替换缓存。
- **按 max_age 保留**：缓存条目在 `max_age` 到期前持续有效；DNS 记录被剥离（返回空）后仍沿用旧策略强制校验，到期后才视为无策略。
- **enforce 强制匹配**：候选 MX 必须命中策略 `mx`；通配仅接受 `*.example.com` 形式（`*` 是完整最左标签）且只覆盖一个标签，`a.b.example.com` 不命中 `*.example.com`；证书 DNS 名（SAN/CN）必须匹配该 MX，同一套单标签通配规则。
- **testing 只记录**：失败写入注入的 logger，投递不阻断，结果带 `testingOverride: true`。
- **失败关闭**：无缓存时策略拉取失败或 DNS 查询报错均返回 `block`，防止降级。

## 用法

```ts
import { MtaStsClient, NodeDnsResolver } from "./index.ts";

const client = new MtaStsClient({ resolver: new NodeDnsResolver() });

const result = await client.evaluate({
  domain: "example.com",
  mxHost: "mail.example.com",
  certificateNames: ["mail.example.com"],
});
// result.action: "deliver" | "block"
// result.reason: "mx-match" | "mx-mismatch" | "certificate-mismatch" | ...
```

投递组件可注入自定义 `DnsResolver`、`HttpsFetcher`、`PolicyCache`、`Logger` 与时钟（便于测试）。`InMemoryPolicyCache` 提供 `snapshot()/load()` 用于跨进程持久化。

## 模块

- `src/hostname.ts`：主机名规范化与严格单标签通配匹配（MX 与证书共用）
- `src/policy.ts`：策略文件与 `_mta-sts` TXT 记录解析
- `src/cache.ts`：按 `max_age` 过期的策略缓存
- `src/client.ts`：拉取、缓存更新与 enforce/testing/none 判定
- `src/node-dns.ts`：基于 `node:dns` 的可选解析器

## 测试

```sh
npm test
```

覆盖多标签通配拒绝、MX 仿冒（`mail.example.com.attacker.net` 等）、证书名不匹配、记录剥离后沿用至到期、id 变化刷新、畸形策略不污染缓存与 testing 只记录等场景。
