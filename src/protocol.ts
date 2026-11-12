/**
 * 跨页面与 SharedWorker 共享的最小协议类型。
 * 刻意不依赖 DOM 类型，便于在 Node 测试环境中直接复用。
 */

export const HANDSHAKE_PROTOCOL = 'shared-worker-tenant-handshake/1' as const;

export type HandshakeMessageType =
  | 'tenant-handshake-request'
  | 'tenant-handshake-result';

/** 握手阶段的错误码，客户端据此区分拒绝原因。 */
export type HandshakeErrorCode =
  | 'invalid_message'
  | 'unknown_tenant'
  | 'token_rejected'
  | 'token_expired'
  | 'token_replayed'
  | 'script_url_mismatch'
  | 'handshake_timeout';

export interface TenantCredentials {
  /** 经认证的租户标识，由可信会话提供，不接受页面自行声明的裸名称。 */
  tenantId: string;
  /** 租户级签名密钥，由后端/配置注入，绝不能由页面内容推导。 */
  secret: string;
}

export interface HandshakeRequestPayload {
  protocol: typeof HANDSHAKE_PROTOCOL;
  type: 'tenant-handshake-request';
  tenantId: string;
  /** 握手令牌（HMAC 签名，绑定租户与脚本 URL，且含一次性 nonce）。 */
  token: string;
  /** 客户端期望连接的 worker 脚本 URL，worker 端必须与自身 URL 比对。 */
  scriptUrl: string;
}

export interface HandshakeOkResult {
  protocol: typeof HANDSHAKE_PROTOCOL;
  type: 'tenant-handshake-result';
  ok: true;
  tenantId: string;
  /** 分配后的业务消息通道端口（通过 transfer 移交）。 */
  port: unknown;
}

export interface HandshakeErrorResult {
  protocol: typeof HANDSHAKE_PROTOCOL;
  type: 'tenant-handshake-result';
  ok: false;
  tenantId: string;
  code: HandshakeErrorCode;
  message: string;
}

export type HandshakeResult = HandshakeOkResult | HandshakeErrorResult;

export type MessageEnvelope = HandshakeRequestPayload | HandshakeResult;

/** 最小消息事件结构，同时兼容 DOM MessageEvent 与测试桩。 */
export interface MessageLike<T = unknown> {
  data: T;
  ports?: unknown[];
}

export type MessageHandler<T = unknown> = (event: MessageLike<T>) => void;

/** 最小端口抽象，浏览器 MessagePort 天然满足该结构。 */
export interface PortLike {
  postMessage(message: unknown, transfer?: unknown[]): void;
  addEventListener(type: string, listener: MessageHandler): void;
  removeEventListener(type: string, listener: MessageHandler): void;
  start(): void;
  close(): void;
}

export interface MessageChannelLike {
  port1: PortLike;
  port2: PortLike;
}

export type MessageChannelFactory = () => MessageChannelLike;

export interface SharedWorkerLike {
  readonly port: PortLike;
}

export type WorkerFactory = (scriptUrl: string, name: string) => SharedWorkerLike;

/** 握手成功后分配给该租户页面的业务通道。 */
export interface TenantChannel {
  tenantId: string;
  port: PortLike;
}

/**
 * worker 侧的租户路由：只有通过握手的端口才会进入该回调，
 * 未通过握手的原始 port 永远不会分配出业务通道。
 */
export type TenantRouter = (channel: TenantChannel) => void;

export interface TenantSecretResolver {
  (tenantId: string): string | undefined;
}

export function isHandshakeRequest(
  value: unknown,
): value is HandshakeRequestPayload {
  if (typeof value !== 'object' || value === null) {
    return false;
  }
  const candidate = value as Record<string, unknown>;
  return (
    candidate.protocol === HANDSHAKE_PROTOCOL &&
    candidate.type === 'tenant-handshake-request' &&
    typeof candidate.tenantId === 'string' &&
    typeof candidate.token === 'string' &&
    typeof candidate.scriptUrl === 'string'
  );
}
