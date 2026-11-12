# SharedWorker 多租户连接代理

同源页面可以通过相同的 `SharedWorker` 名称连到同一个 worker，而 `connect` 事件拿到的
`MessagePort` 不携带页面来源信息，因此“同名即可接入”不能作为信任边界。本库在 TypeScript
中实现可导入的连接代理，把租户隔离从“猜名称”改为“强制握手 + 通道分配”。

## 安全模型

- **名称派生，不接受裸名称**：`SharedWorkerProxy.connect` 只接收经认证的租户凭据与脚本
  URL，内部通过 `deriveWorkerName(tenantId, scriptUrl)` 生成
  `tsw-v1-<sha256>` 名称。不同租户对同一脚本得到不同名称，页面无法传入任意 worker 名。
- **握手先行**：`connect` 得到的原始端口仅用于握手。worker 端
  `TenantWorkerAgent` 对每个新端口只接受一条握手请求；握手未完成前发送的任何业务消息都
  会拒绝并关闭端口，且不会创建业务通道。
- **租户令牌**：令牌为 `base64url(claims).base64url(HMAC-SHA256)`，声明绑定
  `tenantId`、`scriptUrl`、一次性 `nonce`、签发与过期时间。worker 用租户密钥验证签名、
  时效，并通过 `InMemoryReplayCache` 记录 nonce，令牌重放返回 `token_replayed`。
- **脚本 URL 绑定**：请求中的 `scriptUrl` 与令牌内绑定的 `scriptUrl` 都必须与当前 worker
  自身 URL 完全一致，否则返回 `script_url_mismatch`；同名 worker 绑定不同脚本 URL 的构造
  请求则由底层工厂（浏览器或测试运行时）直接拒绝。
- **通过后才分配通道**：全部校验通过后 worker 才 `new MessageChannel()`，把端口登记到该
  租户的路由表，并把另一端移交给页面。

## 目录结构

- `src/protocol.ts`：握手协议、端口/通道抽象与类型守卫
- `src/worker-name.ts`：租户 + 脚本 URL 的 SHA-256 名称派生
- `src/token.ts`：HMAC 握手令牌签发与校验
- `src/replay-cache.ts`：一次性 nonce 重放缓存（可注入时钟）
- `src/worker-agent.ts`：worker 侧握手强制与通道分配
- `src/client-proxy.ts`：页面侧代理与浏览器 `SharedWorker` 工厂
- `src/demo-router.ts`：按租户隔离的示例路由（echo / 同租户广播）
- `src/testing/fake-runtime.ts`：Node 下的内存 SharedWorker / MessageChannel 测试桩
- `examples/`：浏览器 worker 入口与页面接入示例
- `tests/shared-worker-proxy.test.ts`：两个租户、名称复用、令牌重放等样例测试

## 用法

页面侧：

```ts
import { SharedWorkerProxy, createBrowserWorkerFactory } from './src/index.ts';

const proxy = new SharedWorkerProxy(createBrowserWorkerFactory());
const channel = await proxy.connect({
  credentials: { tenantId: 'tenant-a', secret: 'tenant-secret' },
  scriptUrl: new URL('tenant-worker.js', self.location.href).href,
});

channel.port.postMessage({ id: 1, kind: 'echo', payload: 'ping' });
```

worker 侧（见 `examples/browser-worker.ts`）在 `connect` 事件中把端口交给
`TenantWorkerAgent.handleConnectPort`，租户密钥通过受信任配置/后端引导注入，不来自页面消息。

## 测试

```sh
npm test
```

覆盖：名称派生确定性与租户差异、两租户通道隔离与同租户名称复用、握手前消息拒绝、同名不同
脚本 URL 拒绝、令牌重放拒绝、伪造/跨租户令牌拒绝、未知租户与过期令牌、握手超时、底层同名
冲突拒绝，共 9 个用例。
