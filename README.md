# WebFinger / host-meta 账号解析校验

供联邦客户端解析 `acct:` 账号的零依赖 TypeScript 校验包。所有远程数据都按不可信输入处理，校验失败时返回 `null`（fail closed），不会把攻击者控制的主机当作身份来源。

## 用法

```ts
import { resolveAccount } from "./index.ts";

const result = await resolveAccount("acct:alice@example.com", {
  // 仅这些外部主机上的 rel=self 链接会被保留
  allowedSelfHosts: ["cdn.example.com"],
});

if (result) {
  console.log(result.subject);
  console.log(result.links);
}
```

主要导出：

- `resolveAccount(acct, options)`：解析并校验 WebFinger，返回 `AccountResolution | null`。
- `resolveWebFinger(acct, options)`：只返回校验后的 `Jrd | null`。
- `validateJrd(value, queryHost, allowedSelfHosts?)`：校验并过滤 JRD。
- `discoverLrddTemplate(acctOrHost, options)`：抓取并校验 host-meta，返回可用的 lrdd template。
- `validateLrddTemplate(template, queryHost)`：单独校验 lrdd 模板。
- `parseAcct(acct)` / `tryParseAcct(acct)`：解析 `acct:` URI，查询主机只取 acct 的域名。
- `fetchHttpsWithSameHostRedirects(url, options)`：同主机 HTTPS 抓取原语。

## 安全策略

- **仅 HTTPS**：初始请求、重定向目标和 lrdd 模板都必须是 `https:`；带 userinfo 的 URL 一律拒绝。
- **查询主机来自 acct**：只向 `acct:user@host` 中的 `host` 发起请求，不信任响应里给出的其他主机。
- **重定向**：使用 `redirect: "manual"`，仅跟随协议和目标 authority（host:port）均与查询主机相同的跳转；最多两次，第三次直接失败，绝不向外部主机发请求。
- **JRD subject**：必须是合法的 `acct:` URI，且域名与查询域名的 hostname 相同（大小写不敏感，忽略 DNS 末尾点；subject 与查询的端口不参与域名比较）。不满足时整条 JRD 丢弃，而不是只清空链接。
- **host-meta lrdd**：`rel` 包含 `lrdd` 的 `Link` 中，`template` 必须包含字面量 `{uri}`、为绝对 HTTPS URL，且 authority 与查询主机相同。模板变量在 host 位置、userinfo 或协议位置做手脚都会解析失败。
- **`rel=self` 外域链接**：href 解析后的 authority 与查询主机不同时，只有调用方 `allowedSelfHosts` 精确命中（大小写不敏感）才保留；其余 self 链接丢弃。非 self 链接不受此规则影响。
- **超时与异常**：默认 10 秒超时；传输、解析、超时错误都返回 `null`。

`useHostMeta` 默认关闭。开启后：

1. host-meta 返回可用 lrdd 模板时，用该模板构造 WebFinger 请求；
2. host-meta 中存在 lrdd 声明但全部不安全（外域、HTTP、缺 `{uri}` 等）时直接失败，不回退到其他发现路径；
3. host-meta 404、不可达或没有 lrdd 声明时，回退到同主机的标准端点 `https://host/.well-known/webfinger`。

也可以传入 `lrddTemplate` 由调用方指定模板；它仍要经过同样的 `{uri}`、HTTPS、同主机校验。

## 测试

`test/security.test.ts` 覆盖跨主机重定向、超过两次重定向、HTTP 降级、subject 域名不符、外域 lrdd 模板、host-meta 不安全时失败关闭，以及 self 外域链接的白名单过滤。测试传输层可注入，并直接断言攻击者主机从未收到请求。

```sh
npm test
```

运行时要求支持 `--experimental-strip-types` 的 Node.js（当前环境为 Node 23）。
