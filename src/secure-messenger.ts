/**
 * 安全 postMessage 发送组件。
 *
 * 威胁模型：攻击者可在页面向 iframe 发消息前将其导航到恶意站点，
 * 窃取随 postMessage 发出的令牌。本组件的防线：
 *
 * 1. targetOrigin 必须是显式精确来源（协议 + 主机 + 端口），禁止 "*"。
 *    浏览器在投递时按 iframe 当前来源校验，被导航后消息不会送达。
 * 2. 目标为 opaque 来源（如无 allow-same-origin 的 sandbox iframe，
 *    来源序列化为 "null"）时，敏感数据一律不进入 window.postMessage 负载，
 *    改为：随机挑战握手（每个挑战只接受一次）→ 转移 MessagePort →
 *    之后的敏感数据只经 MessagePort 发送。
 */

export const PROTOCOL_VERSION = 1;
export const HELLO_TYPE = "secure-messenger:hello";
export const ACK_TYPE = "secure-messenger:ack";
export const PORT_TYPE = "secure-messenger:port";
export const OPAQUE_ORIGIN = "null";

export interface WindowLike {
  postMessage(message: unknown, targetOrigin: string, transfer?: unknown[]): void;
}

export interface MessageEventLike {
  readonly data: unknown;
  readonly origin: string;
  readonly source: WindowLike | null;
  readonly ports?: readonly unknown[];
}

export interface EventTargetLike {
  addEventListener(type: "message", listener: (event: MessageEventLike) => void): void;
  removeEventListener(type: "message", listener: (event: MessageEventLike) => void): void;
}

export interface MessagePortLike {
  postMessage(message: unknown): void;
  close?(): void;
}

export interface MessageChannelLike {
  port1: MessagePortLike;
  port2: MessagePortLike;
}

export class OriginPolicyError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "OriginPolicyError";
  }
}

export class HandshakeTimeoutError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "HandshakeTimeoutError";
  }
}

/**
 * 校验并返回规范化后的精确来源。
 * 拒绝 "*"、"null"、非 http(s) 协议，以及任何带路径/查询/哈希/用户信息
 * 或可被规范化的写法（如结尾 "/"、默认端口），强制调用方显式书写来源。
 */
export function assertExactOrigin(targetOrigin: string): string {
  if (typeof targetOrigin !== "string" || targetOrigin.length === 0) {
    throw new OriginPolicyError("targetOrigin 必须是非空字符串");
  }
  if (targetOrigin === "*") {
    throw new OriginPolicyError('禁止使用 "*" 作为 targetOrigin：必须显式指定精确来源');
  }
  if (targetOrigin === OPAQUE_ORIGIN) {
    throw new OriginPolicyError('opaque 来源（"null"）不能作为精确 targetOrigin');
  }
  let url: URL;
  try {
    url = new URL(targetOrigin);
  } catch {
    throw new OriginPolicyError(`targetOrigin 不是合法 URL: ${targetOrigin}`);
  }
  if (url.protocol !== "https:" && url.protocol !== "http:") {
    throw new OriginPolicyError(`targetOrigin 仅支持 http(s) 来源: ${targetOrigin}`);
  }
  if (targetOrigin !== url.origin) {
    throw new OriginPolicyError(
      "targetOrigin 必须是精确来源（协议 + 主机 + 端口），不得包含路径、查询、哈希、用户信息或默认端口写法",
    );
  }
  return url.origin;
}

export function isOpaqueOrigin(origin: string): boolean {
  return origin === OPAQUE_ORIGIN;
}

function defaultChallenge(): string {
  const bytes = new Uint8Array(16);
  (globalThis as { crypto: { getRandomValues(b: Uint8Array): void } }).crypto.getRandomValues(bytes);
  let out = "";
  for (const byte of bytes) out += byte.toString(16).padStart(2, "0");
  return out;
}

function defaultChannelFactory(): MessageChannelLike {
  return new (globalThis as { MessageChannel: new () => MessageChannelLike }).MessageChannel();
}

function isEventTargetLike(value: unknown): value is EventTargetLike {
  const candidate = value as EventTargetLike | null;
  return (
    !!candidate &&
    typeof candidate.addEventListener === "function" &&
    typeof candidate.removeEventListener === "function"
  );
}

export interface SecureMessengerOptions {
  /** 目标窗口，或惰性解析函数（发送时才取值，避免持有已导航的旧引用）。 */
  target: WindowLike | (() => WindowLike | null);
  /**
   * 精确目标来源（如 "https://app.example"），禁止 "*"。
   * 传 "null" 表示目标为 opaque 来源（sandbox iframe），走握手 + MessagePort 流程。
   */
  targetOrigin: string;
  /** 发送方自身窗口的 message 事件源（opaque 模式接收握手响应用），默认 globalThis。 */
  messageEvents?: EventTargetLike;
  /** 握手超时，默认 2000ms。 */
  handshakeTimeoutMs?: number;
  channelFactory?: () => MessageChannelLike;
  challengeFactory?: () => string;
}

const DEFAULT_HANDSHAKE_TIMEOUT_MS = 2000;

export class SecureMessenger {
  private readonly resolveTarget: () => WindowLike | null;
  private readonly exactOrigin: string | null = null;
  private readonly opaque: boolean;
  private readonly messageEvents: EventTargetLike | null;
  private readonly handshakeTimeoutMs: number;
  private readonly channelFactory: () => MessageChannelLike;
  private readonly challengeFactory: () => string;
  private readonly consumedChallenges = new Set<string>();
  private port: MessagePortLike | null = null;
  private pendingHandshake: Promise<MessagePortLike> | null = null;

  constructor(options: SecureMessengerOptions) {
    if (options.targetOrigin === OPAQUE_ORIGIN) {
      this.opaque = true;
    } else {
      this.exactOrigin = assertExactOrigin(options.targetOrigin);
      this.opaque = false;
    }
    this.resolveTarget =
      typeof options.target === "function"
        ? options.target
        : () => (options.target as WindowLike) ?? null;
    const events =
      options.messageEvents ?? (isEventTargetLike(globalThis) ? (globalThis as unknown as EventTargetLike) : null);
    if (this.opaque && !events) {
      throw new OriginPolicyError("opaque 模式需要 messageEvents（用于接收握手响应）");
    }
    this.messageEvents = events;
    this.handshakeTimeoutMs = options.handshakeTimeoutMs ?? DEFAULT_HANDSHAKE_TIMEOUT_MS;
    this.channelFactory = options.channelFactory ?? defaultChannelFactory;
    this.challengeFactory = options.challengeFactory ?? defaultChallenge;
  }

  /**
   * 发送消息。精确来源模式直接以该来源调用 postMessage（浏览器在投递时
   * 按目标当前来源校验，iframe 被导航后消息不会送达）；opaque 模式先完成
   * 握手并建立 MessagePort，敏感数据只经端口发送。
   */
  async post(message: unknown, transfer?: unknown[]): Promise<void> {
    const target = this.resolveTarget();
    if (!target) throw new Error("目标窗口不可用");
    if (!this.opaque) {
      target.postMessage(message, this.exactOrigin as string, transfer);
      return;
    }
    if (transfer && transfer.length > 0) {
      throw new OriginPolicyError("opaque 目标不允许随 postMessage 直接 transfer；敏感数据一律经 MessagePort 发送");
    }
    const port = await this.ensureChannel();
    port.postMessage(message);
  }

  close(): void {
    this.port?.close?.();
    this.port = null;
  }

  private ensureChannel(): Promise<MessagePortLike> {
    if (this.port) return Promise.resolve(this.port);
    if (!this.pendingHandshake) {
      this.pendingHandshake = this.runHandshake().finally(() => {
        this.pendingHandshake = null;
      });
    }
    return this.pendingHandshake;
  }

  private runHandshake(): Promise<MessagePortLike> {
    const target = this.resolveTarget();
    if (!target) return Promise.reject(new Error("目标窗口不可用"));
    const events = this.messageEvents;
    if (!events) {
      return Promise.reject(new OriginPolicyError("缺少 messageEvents，无法完成握手"));
    }
    const challenge = this.challengeFactory();
    const channel = this.channelFactory();

    return new Promise<MessagePortLike>((resolve, reject) => {
      const cleanup = () => {
        clearTimeout(timer);
        events.removeEventListener("message", onMessage);
      };
      const onMessage = (event: MessageEventLike) => {
        if (event.source !== (target as unknown)) return;
        const data = event.data as { type?: unknown; challenge?: unknown } | null;
        if (!data || data.type !== ACK_TYPE || data.challenge !== challenge) return;
        if (this.consumedChallenges.has(challenge)) return; // 每个挑战只接受一次
        this.consumedChallenges.add(challenge);
        cleanup();
        // 握手消息与 port 转移均不含敏感数据，opaque 目标只能用 "*" 到达。
        target.postMessage({ v: PROTOCOL_VERSION, type: PORT_TYPE, challenge }, "*", [channel.port2]);
        this.port = channel.port1;
        resolve(channel.port1);
      };
      const timer = setTimeout(() => {
        cleanup();
        reject(new HandshakeTimeoutError(`握手在 ${this.handshakeTimeoutMs}ms 内未完成`));
      }, this.handshakeTimeoutMs);

      events.addEventListener("message", onMessage);
      target.postMessage({ v: PROTOCOL_VERSION, type: HELLO_TYPE, challenge }, "*");
    });
  }
}

export interface HandshakeResponderOptions {
  /** 嵌入页（iframe 内）自身窗口的 message 事件源。 */
  messageEvents: EventTargetLike;
  /** 期望的父页面精确来源（同样禁止 "*"）。 */
  allowedOrigin: string;
  /** 握手完成、收到 MessagePort 后回调。 */
  onChannel: (port: MessagePortLike, event: MessageEventLike) => void;
}

/**
 * 嵌入页一侧的握手响应器。校验父页面来源为精确匹配；每个随机挑战
 * 只接受一次，重复 hello / 重放的 port 消息一律忽略。
 * 返回解绑函数。
 */
export function attachHandshakeResponder(options: HandshakeResponderOptions): () => void {
  const allowedOrigin = assertExactOrigin(options.allowedOrigin);
  const events = options.messageEvents;
  const pendingChallenges = new Set<string>();
  const completedChallenges = new Set<string>();

  const onMessage = (event: MessageEventLike) => {
    const data = event.data as { type?: unknown; challenge?: unknown } | null;
    if (!data || typeof data !== "object") return;
    if (event.origin !== allowedOrigin) return;
    const challenge = data.challenge;
    if (typeof challenge !== "string" || challenge.length === 0) return;

    if (data.type === HELLO_TYPE) {
      if (pendingChallenges.has(challenge) || completedChallenges.has(challenge)) return;
      pendingChallenges.add(challenge);
      // 回包使用父页面的精确来源（已校验），绝不使用 "*"。
      event.source?.postMessage({ v: PROTOCOL_VERSION, type: ACK_TYPE, challenge }, allowedOrigin);
      return;
    }
    if (data.type === PORT_TYPE) {
      if (!pendingChallenges.has(challenge)) return;
      pendingChallenges.delete(challenge);
      completedChallenges.add(challenge);
      const port = event.ports?.[0] as MessagePortLike | undefined;
      if (!port) return;
      options.onChannel(port, event);
    }
  };

  events.addEventListener("message", onMessage);
  return () => events.removeEventListener("message", onMessage);
}
