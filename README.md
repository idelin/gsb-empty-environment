# WebTransport 会话池

供浏览器网络栈调用的 TypeScript WebTransport 会话池。实现的核心安全边界是：
**复用 QUIC 连接不等于复用 WebTransport 会话授权**。

## 身份绑定

连接池查找键 `ConnectionIdentity.transportKey` 由以下字段构成，不包含 origin：

- network partition key
- 代理链（有序）
- TLS 栈验证后的证书身份（指纹 + 已验证 DNS/IP SAN）
- ECH 结果（未尝试 / 接受及 config ID / 拒绝及 retry config ID）
- ALPN
- 远端端点（IP 族、地址、端口）

完整 `ConnectionIdentity` 另绑定 QUIC 代次；代次变化会淘汰旧连接的新分配资格。

因此同一证书覆盖的两个 origin 可以落在同一条底层连接上，但不会自动获得对方
的会话权限。每个 `SessionHandle` 背后的 session 记录另行绑定请求 origin、
去 fragment 后的规范 URL、服务器返回的 draft/version、session ID，以及独立的
stream/datagram 配额。

## 授权规则

在已有连接上创建新会话必须同时满足：

1. 已验证证书覆盖请求 origin；
2. 请求 origin 等于连接建立时的同源 origin，**或**该 origin 出现在由该连接
   已认证 HTTP/3 control stream 收到且通过校验的 ORIGIN frame 中。

ORIGIN frame 只有在 `generation` 匹配、来源标记为
`http3-control-stream`、且其中每个 origin 都被当前证书覆盖时才会被整帧接受。
伪造、过期、跨代次或夹带未覆盖 origin 的 frame 会被原子拒绝。应用层
JavaScript 不应接触该 API；这是网络栈内部信任边界。

连接级共享仅限协议簿记：session ID 分配/占用检查、stream ID 映射和拥塞统计。
URL、quota、datagram 队列、流集合以及后续应用状态均保存在 session 内，
不会因连接合并而跨 origin 可见。

## 冻结与路由

以下事件冻结该连接的新会话分配：

- `handleGoAway` / `receiveGoAway`
- `handleCertificateUpdate` / `notifyCertificateUpdate`
- `handleNetworkSwitch` / `notifyNetworkSwitch`
- `rejectZeroRtt` / `handleZeroRttRejection`

创建会话在异步 CONNECT 前后都会重新检查权威与冻结状态，因此 GOAWAY、
证书更新、网络切换或 0-RTT 拒绝发生在 CONNECT 途中也不会提交新会话。
0-RTT 拒绝还会关闭 `earlyData` 会话；1-RTT 会话可继续。GOAWAY 只阻止新分配，
已有会话可继续排空。

迟到 datagram 以 `(QUIC generation, session ID)` 作为路由键。旧代次、未知 ID、
已关闭 ID 均静默丢弃；同一 session ID 可在新代次复用，但绝不会路由到新代次。
`quicGeneration` 省略时由池分配；显式值必须在池内唯一，避免
`(generation, session ID)` 路由键歧义。

## 用法

```ts
import { WebTransportSessionPool } from "./index.ts";

const pool = new WebTransportSessionPool();
const connection = pool.openConnection({
  networkPartitionKey: "partition-1",
  proxyChain: ["direct"],
  certificate: {
    fingerprintSha256: "sha256:...",
    dnsNames: ["a.example", "b.example"],
    ipAddresses: [],
  },
  ech: { status: "not_offered" },
  alpn: "h3",
  remoteEndpoint: { address: "203.0.113.10", port: 443 },
  quicGeneration: 1,
  initialOrigin: "https://a.example",
});

const sessionA = await pool.createSession({
  connectionId: connection.id,
  url: "https://a.example/transport",
  establishment: {
    sessionId: "session-a",
    draft: "draft-ietf-webtransport-http3-13",
    version: "13",
    streamLimit: 8,
    datagramLimit: 32,
  },
});

// 仅当 b.example 同时被证书覆盖且出现在已认证 ORIGIN frame 时才成功。
pool.receiveOriginFrame(connection.id, {
  generation: connection.generation,
  origins: ["https://b.example"],
  source: "http3-control-stream",
});

await pool.createSession({
  connectionId: connection.id,
  url: "https://b.example/transport",
  establishment: { /* ... */ },
});

pool.deliverDatagram({
  quicGeneration: sessionA.quicGeneration,
  sessionId: sessionA.sessionId,
  payload: new Uint8Array([1, 2, 3]),
});
```

`establishSession` 构造函数钩子可延迟服务器 CONNECT 响应，用于测试冻结竞态；
`abortSession` 在会话提交失败时执行清理。

## 验证

`npm test` 覆盖：

- 同证书两个 origin 默认不能互相建会话，合法 ORIGIN frame 后可共连；
- 伪/过期/夹带未覆盖 origin 的 ORIGIN frame 被整帧拒绝；
- session ID 在同连接内禁用复用，跨代次复用时迟到 datagram 不串会话；
- 网络切换冻结新分配并丢弃旧代次 datagram；
- GOAWAY 在 CONNECT 途中到达时竞态失败、已有会话继续排空；
- 0-RTT 拒绝关闭 early-data 会话并冻结新分配；
- 配额、stream 集合、datagram 队列按 origin 隔离，拥塞统计按连接共享。

运行：`npm test`
