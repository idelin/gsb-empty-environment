import { TenantWorkerAgent } from '../src/worker-agent.ts';
import { DemoTenantRouter } from '../src/demo-router.ts';
import type { PortLike, TenantSecretResolver } from '../src/protocol.ts';

/**
 * 浏览器中的 SharedWorker 入口（由构建工具打包成独立 worker 脚本）。
 * 租户密钥应由受信任配置/后端引导，而不是页面消息传入。
 */
const tenantSecrets: Record<string, string> = {
  'tenant-a': 'replace-with-a-real-secret-a',
  'tenant-b': 'replace-with-a-real-secret-b',
};

const resolveSecret: TenantSecretResolver = (tenantId) =>
  tenantSecrets[tenantId];

const router = new DemoTenantRouter();

const agent = new TenantWorkerAgent({
  scriptUrl: self.location.href,
  resolveSecret,
  onChannel: router.onChannel,
  createMessageChannel: () => new MessageChannel(),
});

interface SharedWorkerConnectEvent extends Event {
  readonly ports: ReadonlyArray<PortLike>;
}

self.addEventListener('connect', (event) => {
  const connectEvent = event as SharedWorkerConnectEvent;
  for (const port of connectEvent.ports) {
    agent.handleConnectPort(port);
  }
});
