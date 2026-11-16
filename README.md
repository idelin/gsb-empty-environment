# Sparkplug MQTT ACL

一个可直接导入的 TypeScript Sparkplug B ACL 判定包，无运行时依赖。它把
MQTT 登录身份、Sparkplug 主题、节点 `bdSeq` 状态和 Rebirth 频率组合成一次
同步判定，适合在 MQTT 代理的 `publish` / `subscribe` 回调中调用。

## 策略

| 主体 | 发布 | 订阅 |
| --- | --- | --- |
| 主应用凭据 | 仅 `spBv1.0/<group>/NCMD/<node>` 与 `spBv1.0/<group>/DCMD/<node>/<device>` | `spBv1.0/#` 范围内 |
| 边缘节点 | 仅自己的 `spBv1.0/<own-group>/NBIRTH/<own-client-id>`、`NDATA`、`NDEATH` | 仅自己的 `NCMD`，以及自己节点下的 `DCMD` |
| 未知主体 | 拒绝 | 拒绝 |

边缘节点的主题组必须等于注册组，第四级节点名必须等于 MQTT 客户端标识。
`NCMD` / `DCMD` 不能由边缘节点发布，通配符不能出现在发布主题中。

`NDEATH` 只有在同一组、同一节点已经发布过 `NBIRTH`，且 payload 中的
`bdSeq` 与最近一次已接受 `NBIRTH` 的 `bdSeq` 完全一致时才允许。最近一次
`NBIRTH` 未带 `bdSeq` 时，后续 `NDEATH` 会被拒绝。

Rebirth 命令按目标节点计数，默认使用 60 秒滑动窗口，每个节点最多 3 次。
被 ACL 拒绝的尝试不占用配额；不同节点、不同组之间互不影响。

## 使用

```ts
import { SparkplugAcl } from "./index.ts";

const acl = new SparkplugAcl({
  application: {
    username: "primary-app",
    password: "app-secret",
  },
  edgeNodes: [
    {
      clientId: "edge-a",
      groupId: "plant-a",
      username: "edge-a",
      password: "edge-secret",
    },
  ],
});

const publishDecision = acl.authorize({
  action: "publish",
  clientId: "edge-a",
  username: "edge-a",
  password: "edge-secret",
  topic: "spBv1.0/plant-a/NBIRTH/edge-a",
  payload: {
    metrics: [{ name: "bdSeq", value: 7n }],
  },
});

if (!publishDecision.allow) {
  console.error(publishDecision.reason, publishDecision.detail);
}
```

常用的便捷方法：

```ts
acl.authorizePublish({ clientId, username, password, topic, payload });
acl.authorizeSubscribe({ clientId, username, password, topic });
acl.canPublish(request);
acl.canSubscribe(request);
```

`authorizePublish(clientId, topic, payload)` 这种简写形式也可用，但配置了
边缘节点用户名/密码时应传入完整凭据对象。

判定结果包含以下字段：

```ts
{
  allow: boolean;
  allowed: boolean;
  ok: boolean;
  reason: string;
  code: string;
  detail?: string;
  retryAfterMs?: number;
}
```

限流拒绝的 `reason` 为 `rebirth-rate-limited`，并通过 `retryAfterMs` 给出
最早可重试时间。

## Payload

`bdSeq` 与 `Node Control/Rebirth` 支持以下形式：

- 解码后的 Sparkplug 对象：`{ metrics: [{ name: "bdSeq", value: 7 }] }`
- JSON 字符串
- 原始 Sparkplug B protobuf：`Uint8Array`、`ArrayBuffer` 或 Node `Buffer`
- `bdSeq` 的 `number`、`bigint`、十进制字符串或 protobuf `Long` 形式

如果同一 payload 中同时存在多个不一致的 `bdSeq` 值，判定为无效。

## 状态与配置

```ts
const acl = new SparkplugAcl({
  application: { username: "primary-app", password: "app-secret" },
  edgeNodes: {
    "edge-a": { groupId: "plant-a", username: "edge-a", password: "edge-secret" },
  },
  rebirthLimit: 3,
  rebirthWindowMs: 60_000,
  now: () => Date.now(),
});
```

`lastBdSeq(groupId, clientId)` 可读取节点最近一次 `NBIRTH` 的序号；
`reset()` 清空节点出生状态和限流历史。多进程或集群部署时，`bdSeq` 与
Rebirth 计数需要放入共享状态存储，或保证同一节点的判定落在同一实例。

## 安全边界

- 该包只做 ACL 判定，不替代 MQTT 身份认证。`principal`、`role` 字段来自
  已认证的代理上下文，不能直接映射自客户端可控字段。
- MQTT 代理和 Sparkplug 消费者都必须执行同一份判定；ACL 返回值本身不会
  阻止消息发布。
- 边缘节点的 WILL 主题也必须经过同一套 `NDEATH` 校验。
- 密码比较仅用于本地的凭据归属判定；生产环境应由代理的认证插件验证密码，
  并向本包传入已认证的身份。

## 测试

```sh
npm test
```

测试覆盖越权发布/订阅、跨节点伪造 `NDEATH`、错误 `bdSeq`、多 `bdSeq`
冲突、JSON 与 protobuf payload，以及 Rebirth 滑动窗口洪泛。
