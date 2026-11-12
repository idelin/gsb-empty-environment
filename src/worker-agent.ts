import { InMemoryReplayCache } from './replay-cache.ts';
import { verifyHandshakeToken } from './token.ts';
import {
  HANDSHAKE_PROTOCOL,
  isHandshakeRequest,
  type HandshakeErrorCode,
  type HandshakeRequestPayload,
  type MessageChannelFactory,
  type MessageHandler,
  type PortLike,
  type TenantRouter,
  type TenantSecretResolver,
} from './protocol.ts';

export interface WorkerAgentOptions {
  /** 当前 worker 脚本自身的 URL，用于拒绝同名但不同脚本的连接。 */
  scriptUrl: string;
  resolveSecret: TenantSecretResolver;
  onChannel: TenantRouter;
  createMessageChannel: MessageChannelFactory;
  replayCache?: InMemoryReplayCache;
  /** 握手超时（毫秒），默认 10 秒；超时关闭原始端口且不分配通道。 */
  handshakeTimeoutMs?: number;
  /** 注入当前时间，测试可替换。 */
  now?: () => number;
}

interface PendingHandshake {
  timer: ReturnType<typeof setTimeout>;
}

const pendingByPort = new WeakMap<PortLike, PendingHandshake>();

/**
 * worker 侧连接代理。
 * 对每个新接入的原始 port 强制执行握手：
 * 1. 校验消息结构；2. 校验租户令牌签名/时效/重放；
 * 3. 校验请求脚本 URL 与本 worker 完全一致；
 * 全部通过后才 new MessageChannel 分配业务通道。
 */
export class TenantWorkerAgent {
  private readonly replayCache: InMemoryReplayCache;
  private readonly handshakeTimeoutMs: number;
  private readonly now: () => number;

  private readonly options: WorkerAgentOptions;

  constructor(options: WorkerAgentOptions) {
    this.options = options;
    this.now = options.now ?? (() => Date.now());
    this.replayCache =
      options.replayCache ?? new InMemoryReplayCache(4096, this.now);
    this.handshakeTimeoutMs = options.handshakeTimeoutMs ?? 10_000;
  }

  /** 适配 SharedWorker connect 事件，也可直接传入测试端口。 */
  handleConnectPort(port: PortLike): void {
    const timer = setTimeout(() => {
      this.reject(
        port,
        '',
        'handshake_timeout',
        'handshake did not complete in time',
      );
    }, this.handshakeTimeoutMs);
    pendingByPort.set(port, { timer });

    const handler: MessageHandler = (event) => {
      void this.handleMessage(port, event, handler);
    };
    port.addEventListener('message', handler);
    port.start();
  }

  private async handleMessage(
    port: PortLike,
    event: { data: unknown },
    handler: MessageHandler,
  ): Promise<void> {
    if (!isHandshakeRequest(event.data)) {
      this.reject(port, '', 'invalid_message', 'expected a handshake request');
      return;
    }
    const request = event.data;
    port.removeEventListener('message', handler);

    const secret = this.options.resolveSecret(request.tenantId);
    if (secret === undefined) {
      this.reject(port, request.tenantId, 'unknown_tenant', 'tenant not registered');
      return;
    }

    const verification = await verifyHandshakeToken(request.token, secret, {
      now: this.now(),
      isReplayed: (nonce) => this.replayCache.has(nonce),
      rememberNonce: (nonce, expiresAt) => this.replayCache.add(nonce, expiresAt),
    });
    if (!verification.ok) {
      this.reject(port, request.tenantId, verification.reason, 'handshake token rejected');
      return;
    }

    if (verification.claims.tenantId !== request.tenantId) {
      this.reject(port, request.tenantId, 'token_rejected', 'token tenant mismatch');
      return;
    }

    if (request.scriptUrl !== this.options.scriptUrl) {
      this.reject(
        port,
        request.tenantId,
        'script_url_mismatch',
        'requested script URL does not match this worker',
      );
      return;
    }

    if (verification.claims.scriptUrl !== this.options.scriptUrl) {
      this.reject(
        port,
        request.tenantId,
        'script_url_mismatch',
        'token is bound to a different script URL',
      );
      return;
    }

    this.accept(port, request);
  }

  private accept(port: PortLike, request: HandshakeRequestPayload): void {
    const pending = pendingByPort.get(port);
    if (pending !== undefined) {
      clearTimeout(pending.timer);
      pendingByPort.delete(port);
    }

    const channel = this.options.createMessageChannel();
    this.options.onChannel({
      tenantId: request.tenantId,
      port: channel.port1,
    });

    port.postMessage(
      {
        protocol: HANDSHAKE_PROTOCOL,
        type: 'tenant-handshake-result',
        ok: true,
        tenantId: request.tenantId,
        port: channel.port2,
      },
      [channel.port2],
    );
  }

  private reject(
    port: PortLike,
    tenantId: string,
    code: HandshakeErrorCode,
    message: string,
  ): void {
    const pending = pendingByPort.get(port);
    if (pending !== undefined) {
      clearTimeout(pending.timer);
      pendingByPort.delete(port);
    }
    try {
      port.postMessage({
        protocol: HANDSHAKE_PROTOCOL,
        type: 'tenant-handshake-result',
        ok: false,
        tenantId,
        code,
        message,
      });
    } finally {
      port.close();
    }
  }
}
