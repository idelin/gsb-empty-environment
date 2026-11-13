# Sparkplug 主题权限与遗嘱序号绑定

面向 TypeScript MQTT 代理的可导入 ACL 判定包。它在代理完成身份认证之后执行，
不信任客户端自报的主题、节点名或角色。

## 判定规则

- `NCMD` / `DCMD` 只允许 `kind: "application"` 的主应用凭据发布；边缘节点发布命令直接拒绝。
- 边缘节点只能发布 `spBv1.0/<自己的 groupId>/NBIRTH|NDATA|NDEATH`，主题中的 node id 必须等于认证后的 `clientId`。
- `NDATA` 可带设备级第 5 段；`NBIRTH` / `NDEATH` 只能寻址边缘节点本身。
- 被接受的 `NBIRTH` 会记录该 group/node 的最新 `bdSeq`；`NDEATH` 必须携带相同 `bdSeq`。无 NBIRTH、缺失/非法 bdSeq、序号不一致都拒绝。
- 同一 group/node 的 Rebirth 命令使用 60 秒滑动窗口，最多 3 次；第 4 次拒绝。不同节点配额独立，非 Rebirth 命令不计入。
- 边缘节点不能订阅 `NCMD` / `DCMD`，也不能用 `+` / `#` 通配符绕到命令主题。

## 用法

```ts
import { SparkplugAcl } from "./index.ts";

const acl = new SparkplugAcl({
  maxRebirthCommandsPerMinute: 3,
  rebirthWindowMs: 60_000,
});

// 这些身份必须来自代理的认证结果。
const application = { kind: "application", clientId: "primary-host" } as const;
const edge = {
  kind: "edge-node",
  clientId: "node-1",
  groupId: "plant-a",
} as const;

const birth = acl.evaluatePublish(
  edge,
  "spBv1.0/plant-a/NBIRTH/node-1",
  {
    metrics: [{ name: "bdSeq", type: "Int64", value: 7 }],
  },
);

const death = acl.evaluatePublish(
  edge,
  "spBv1.0/plant-a/NDEATH/node-1",
  {
    metrics: [{ name: "bdSeq", type: "Int64", value: 7 }],
  },
);

const rebirth = acl.evaluatePublish(
  application,
  "spBv1.0/plant-a/NCMD/node-1",
  {
    metrics: [
      { name: "Node Control/Rebirth", type: "Boolean", value: true },
    ],
  },
);

if (!death.allowed) {
  console.error(death.code, death.reason, death.detail);
}
```

返回值：

```ts
interface AclDecision {
  allowed: boolean;
  code: string;
  reason: string;
  detail?: string;
}
```

代理回调可直接使用布尔别名：`authorizePublish` / `canPublish` 和
`authorizeSubscribe` / `canSubscribe`。

## Payload 支持

`bdSeq` 与 Rebirth 指标可从以下输入识别：

- Sparkplug B protobuf 二进制（`Buffer`、`Uint8Array`、`ArrayBuffer`）
- Tahu 风格 JSON 对象或 JSON 字符串
- 编码为 JSON 的 `Buffer` / `Uint8Array`
- 轻量集成使用的直接数值 `bdSeq`

JSON 指标支持 `longValue` / `long_value` / `value`，并识别 `type`、
`dataType`、`datatype` 中的 `Int64`、`Boolean` 等类型。

## 集成约束

- 每个 broker 进程持有最新 NBIRTH bdSeq 与限流窗口。集群部署需要把二者放到共享存储，或让节点固定路由到同一实例。
- `evaluatePublish` 在校验通过时会更新状态，因此每个发布授权请求只调用一次。只读检查请用导出解析函数。
- 主应用与边缘节点的 `kind` 必须由认证层设置；不要根据 MQTT payload 或 topic 推断角色。

## 验证

```sh
npm test
```

测试覆盖越权发布、跨组/跨节点冒充、伪造 NDEATH、缺失/错误 bdSeq、Rebirth 命令洪泛、命令订阅与通配符订阅，以及 JSON/Protobuf payload。
