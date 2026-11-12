# Whois转介跟随的主机与协议限定

## 安全边界

- `parseReferral` 只扫描第一个空行之前的首部块，正文或注册人备注中的同名字段会被忽略。
- 转介目标只接受主机名和 43 端口；拒绝 IP 字面量、userinfo、路径、查询串、fragment 和其他端口。
- `resolveReferral` 要求 DNS 返回的全部地址都是全局单播地址，并保留实际地址族。
- `createReferralSocket` 只使用已校验的 IP 地址和端口 43 建立 TCP 连接，不重新使用主机名发起连接。
- `validateQuery` 拒绝 CR、LF、NUL、以 `-` 开头的查询以及超过 255 UTF-8 字节的查询。
- `ReferralChain` 最多跟随三次，并拒绝回到初始主机或任何已访问过的转介主机。

## 查询流程示例

```ts
import {
  ReferralChain,
  parseReferral,
  queryReferral,
  resolveReferral,
} from "./index.ts";

const chain = new ReferralChain("whois.iana.org");
let response = "ReferralServer: whois://rr.example.com:43\r\n\r\n";

for (;;) {
  const next = await chain.follow(response, "example.com");
  if (next === null) break;
  response = next.response;
}
```

底层步骤也可以单独调用：

```ts
const referral = parseReferral(response);
if (referral) {
  const resolved = await resolveReferral(referral);
  const registrarResponse = await queryReferral(resolved, "example.com");
}
```

运行测试：

```sh
npm test
```
