import { handshakeError, WorkerProxyError } from './errors.ts';
import { issueHandshakeToken } from './token.ts';
import { deriveWorkerName } from './worker-name.ts';
import {
  HANDSHAKE_PROTOCOL,
  type HandshakeResult,
  type MessageHandler,
  type PortLike,
  type SharedWorkerLike,
  type TenantCredentials,
  type WorkerFactory,
} from './protocol.ts';

export interface ConnectRequest {
  credentials: TenantCredentials;
  /** 目标 SharedWorker 脚本 URL（令牌与 worker 名称都绑定该值）。 */
  scriptUrl: string;
  /** 握手超时（毫秒），默认 10 秒。 */
  timeoutMs?: number;
  /** 注入令牌签发时间/nonce，测试可替换。 */
  now?: number;
  nonce?: string;
}

export interface ConnectedTenantChannel {
  tenantId: string;
  workerName: string;
  /** 握手通过后由 worker 分配的业务消息端口。 */
  port: PortLike;
  /** 关闭握手控制端口与业务端口。 */
  close(): void;
}

/**
 * 页面侧连接代理：
 * - 仅接受经认证的租户凭据与脚本 URL，名称在内部派生，不接受裸名称；
 * - 拿到的原始 port 不承载业务消息，必须先完成带租户令牌的握手；
 * - 只有握手成功才返回分配出的消息通道。
 */
export class SharedWorkerProxy {
  private readonly createWorker: WorkerFactory;

  constructor(createWorker: WorkerFactory) {
    this.createWorker = createWorker;
  }

  async connect(request: ConnectRequest): Promise<ConnectedTenantChannel> {
    const { credentials, scriptUrl } = request;
    if (!credentials.tenantId) {
      throw new WorkerProxyError('tenantId is required', 'invalid_message');
    }

    const workerName = await deriveWorkerName(credentials.tenantId, scriptUrl);
    const { token } = await issueHandshakeToken(credentials, scriptUrl, {
      now: request.now,
      nonce: request.nonce,
    });

    let worker: SharedWorkerLike;
    try {
      worker = this.createWorker(scriptUrl, workerName);
    } catch (cause) {
      throw new WorkerProxyError(
        'failed to create or reuse SharedWorker',
        'factory_error',
        { cause },
      );
    }

    const controlPort = worker.port;
    const allocatedPort = await this.performHandshake(
      controlPort,
      {
        protocol: HANDSHAKE_PROTOCOL,
        type: 'tenant-handshake-request',
        tenantId: credentials.tenantId,
        token,
        scriptUrl,
      },
      request.timeoutMs ?? 10_000,
    );

    return {
      tenantId: credentials.tenantId,
      workerName,
      port: allocatedPort,
      close: () => {
        allocatedPort.close();
        controlPort.close();
      },
    };
  }

  private performHandshake(
    controlPort: PortLike,
    payload: unknown,
    timeoutMs: number,
  ): Promise<PortLike> {
    return new Promise((resolve, reject) => {
      const timer = setTimeout(() => {
        cleanup();
        controlPort.close();
        reject(handshakeError('handshake_timeout', 'handshake timed out'));
      }, timeoutMs);

      const handler: MessageHandler = (event) => {
        const result = event.data as Partial<HandshakeResult> | undefined;
        if (
          result?.protocol !== HANDSHAKE_PROTOCOL ||
          result?.type !== 'tenant-handshake-result'
        ) {
          return;
        }
        clearTimeout(timer);
        controlPort.removeEventListener('message', handler);
        if (result.ok !== true) {
          const errorResult = result as Extract<
            HandshakeResult,
            { ok: false }
          >;
          controlPort.close();
          reject(handshakeError(errorResult.code, errorResult.message));
          return;
        }
        const transferred = this.extractPort(event, result as HandshakeResult);
        if (transferred === undefined) {
          controlPort.close();
          reject(new WorkerProxyError('worker did not allocate a port', 'no_port'));
          return;
        }
        resolve(transferred);
      };

      const cleanup = () => controlPort.removeEventListener('message', handler);
      controlPort.addEventListener('message', handler);
      controlPort.start();
      controlPort.postMessage(payload);
    });
  }

  private extractPort(
    event: { ports?: unknown[] },
    result: HandshakeResult,
  ): PortLike | undefined {
    const transferred = event.ports?.[0];
    if (this.isPort(transferred)) {
      return transferred;
    }
    const inline = (result as { port?: unknown }).port;
    return this.isPort(inline) ? inline : undefined;
  }

  private isPort(value: unknown): value is PortLike {
    return (
      typeof value === 'object' &&
      value !== null &&
      typeof (value as PortLike).postMessage === 'function' &&
      typeof (value as PortLike).addEventListener === 'function' &&
      typeof (value as PortLike).close === 'function'
    );
  }
}

/** 浏览器环境工厂：使用原生 SharedWorker，名称由代理派生后传入。 */
export function createBrowserWorkerFactory(): WorkerFactory {
  return (scriptUrl, name) =>
    new SharedWorker(scriptUrl, {
      name,
      credentials: 'same-origin',
    }) as unknown as SharedWorkerLike;
}
