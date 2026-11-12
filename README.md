# RDAP 应答与引导来源准入

域名风控服务通过 RDAP 查询注册信息。攻击者控制的注册商可在应答的 `links`
里把后续请求引向内网地址，或返回与查询对象不一致的实体。本包是供查询流程
直接调用的“应答准入层”，把所有外发出口与实体一致性策略收敛到一处，零第三方
依赖，仅使用 Node 内置模块，可在 `node --test`（原生 TS 类型擦除）下运行。

## 威胁与对应控制

| 威胁 | 控制 |
| --- | --- |
 | 服务器被换成非 IANA 授权主机 | 查询基址只从 IANA `dns.json` 当前内容派生；TLD 引导缺项直接拒绝，不退化到默认服务器 |
| `links`/重定向引向内网 | 仅允许 `https`；主机必须命中引导服务器集合或调用方白名单；IP 字面量命中私有/环回/链路本地/CGNAT/保留段即拒绝（含 `169.254.169.254` 云元数据、IPv4 映射 IPv6） |
| 重定向绕过 | 手动逐跳处理 3xx，每一跳重新做相同准入，最多 3 次，超出即 `redirect-limit-exceeded` |
| IDN 大小写混淆 | `ldhName` 与查询名都经 `URL.domainToASCII` 归一为**小写 Punycode** 后再比较 |
| 返回错类型/不一致实体 | 校验 `objectClassName === "domain"` 与 `rdapConformance` 含 `rdap_level_0`，任一不符整条丢弃 |
| 脏数据污染缓存 | `AdmissionCache.put` 仅在 `admitResponse` 通过时写入；未准入对象物理上无法进入缓存 |

## 模块

- `src/netsec.ts` — URL/主机准入原语：`parseHttpsUrl`、`evaluateUrl`、`isPrivateOrNonGlobal`、`isAllowedHost`
- `src/bootstrap.ts` — IANA 引导解析与 TLD 匹配：`BootstrapRegistry`、`parseBootstrap`
- `src/domain.ts` — 小写 Punycode 归一化：`normalizeDomain`、`sameDomain`
- `src/admission.ts` — 实体准入与链接过滤：`admitResponse`、`filterFollowableLinks`、`buildRdapUrl`
- `src/client.ts` — 查询流程封装：`RdapClient`、`AdmissionCache`、`RdapAdmissionError`

## 用法

```ts
import {
  AdmissionCache,
  BootstrapRegistry,
  IANA_DNS_BOOTSTRAP_URL,
  RdapClient,
} from "./index.ts";

const registry = new BootstrapRegistry();

// 生产环境：拉取 IANA 引导文件“当前内容”。可注入带代理/超时的 fetch。
await registry.refresh(fetch, IANA_DNS_BOOTSTRAP_URL);

const client = new RdapClient({
  registry,
  cache: new AdmissionCache(15 * 60 * 1000),
  hostWhitelist: ["partner.rdap.example"], // 调用方额外白名单，仍强制 https 且禁私有地址
  // fetchImpl: fetch, // 可注入，便于测试或统一加超时/审计
});

const outcome = await client.query("Bücher.com"); // Unicode/大写 Punycode 等价
// outcome.body 已通过 objectClassName / ldhName / rdapConformance 校验并已入缓存
const safeLinks = client.safeLinks(outcome.body); // 内网/非 https/未授权链接已被剔除
```

失败以 `RdapAdmissionError` 抛出，`failure` 取值：
`invalid-query-name`、`bootstrap-not-loaded`、`bootstrap-missing`、`request-failed`、
`redirect-limit-exceeded`、`redirect-not-https`、`redirect-host-not-allowed`、
`redirect-private-address`、`bad-status`、`admission-rejected`。

## 测试

```sh
npm test
```

`test/` 下用内存 `mockFetch` 与离线引导夹具覆盖：内网/环回/链路本地链接拦截、
IDN 大小写与 Unicode 等价、错类型对象与 `ldhName` 不一致整条丢弃且不缓存、
引导缺项拒绝、重定向 3 次上限及白名单语义，共 35 个用例。
