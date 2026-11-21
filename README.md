# 长连接网关握手来源校验组件

面向 WebSocket 风格长连接升级握手的来源（Origin）与票据校验组件，纯 TypeScript、
零运行时依赖，使用 Node 内置测试运行器验证。

## 校验规则

- 来源核对：握手必须携带 `Origin`，按 scheme/host/port（含默认端口归一化）与本站
  （`siteOrigin`）比较；跨站请求再与受信站点清单（`trustedOrigins`）核对，不在清单中
  报 `untrusted_origin`，在清单中仍拒绝升级并报 `cross_site_upgrade_rejected`。
- 一次性票据：握手必须携带 `sec-websocket-ticket`；票据含随机 `nonce`、绑定的
  `origin`、`scope` 与 `expiresAt`。`InMemoryTicketStore.consume` 原子消费 nonce，
  重复使用报 `ticket_already_used`，过期报 `ticket_expired`。
- 票据作用域：票据绑定的 origin 必须与请求 Origin 同源（`ticket_origin_mismatch`），
  scope 必须与握手路径一致（`ticket_scope_mismatch`），防止票据跨站点/跨服务挪用。
- 子协议协商：解析 `sec-websocket-protocol`，在 `acceptedSubprotocols` 中按客户端
  偏好顺序选第一个共同支持的协议；客户端有提议但全部不被接受时报
  `unacceptable_subprotocol`。
- 压缩扩展：解析 `sec-websocket-extensions`；同站允许 `permessage-deflate`，
  跨站握手携带该扩展时报 `cross_site_compression_rejected`（压缩可被 BREACH 类攻击利用）。
- 代理转发：出现 `Forwarded` / `X-Forwarded-Host` / `X-Forwarded-Proto` 时，只有
  `trustedProxy` 开启才接受，否则报 `untrusted_proxy_headers`；受信代理转发的目标
  主机不是本站时报 `proxy_cross_site_forward`，阻断代理携带的跨站升级。

## 模块结构

- `src/verifier.ts`：`HandshakeOriginVerifier` 编排完整握手校验流程。
- `src/origin.ts`：Origin 解析与同源比较（默认端口归一化）。
- `src/ticket.ts`：票据签发、JSON 解析、作用域匹配与内存一次性票据存储。
- `src/extensions.ts`：子协议与扩展头解析、子协议协商。
- `src/proxy.ts`：RFC 7239 `Forwarded` 与 `X-Forwarded-*` 解析。
- `src/errors.ts`：`HandshakeRejected` 与稳定错误码枚举。
- `test/`：覆盖跨站、重放、过期、作用域、子协议、压缩与代理转发的测试。

## 运行测试

```sh
npm test
```

## 使用示例

```ts
import {
   HandshakeOriginVerifier,
   HandshakeTicketIssuer,
   InMemoryTicketStore,
} from './src/index.ts';

const ticketStore = new InMemoryTicketStore();
const issuer = new HandshakeTicketIssuer({ ttlMs: 60_000 });
const verifier = new HandshakeOriginVerifier({
  siteOrigin: 'https://gateway.example.com',
  trustedOrigins: ['https://partner.example.com'],
  acceptedSubprotocols: ['chat.v2'],
  ticketStore,
  trustedProxy: true,
});

// 业务侧先签发票据并交给受信页面
ticketStore.add(issuer.issue('https://gateway.example.com', '/realtime/gateway'));

const accepted = verifier.verify({
  scope: '/realtime/gateway',
  headers: handshakeHeaders,
});
```
