import type { PortLike, TenantChannel, TenantRouter } from './protocol.ts';

interface RpcRequest {
  id: number;
  kind: string;
  payload?: unknown;
}

/**
 * 演示用租户路由：仅对已通过握手的通道生效，
 * 按租户维护通道表，支持 echo 与同租户广播，天然跨租户隔离。
 */
export class DemoTenantRouter {
  private readonly channelsByTenant = new Map<string, Set<PortLike>>();

  readonly onChannel: TenantRouter = (channel: TenantChannel) => {
    this.register(channel);
  };

  channelsFor(tenantId: string): number {
    return this.channelsByTenant.get(tenantId)?.size ?? 0;
  }

  private register(channel: TenantChannel): void {
    let set = this.channelsByTenant.get(channel.tenantId);
    if (set === undefined) {
      set = new Set<PortLike>();
      this.channelsByTenant.set(channel.tenantId, set);
    }
    set.add(channel.port);

    const handler = (event: { data: unknown }) => {
      const message = event.data as RpcRequest;
      if (message?.kind === 'echo') {
        channel.port.postMessage({
          id: message.id,
          kind: 'echo-result',
          tenantId: channel.tenantId,
          payload: message.payload,
        });
        return;
      }
      if (message?.kind === 'broadcast') {
        for (const peer of set ?? []) {
          peer.postMessage({
            kind: 'broadcast',
            tenantId: channel.tenantId,
            payload: message.payload,
          });
        }
      }
    };
    channel.port.addEventListener('message', handler);
    channel.port.start();
  }
}
