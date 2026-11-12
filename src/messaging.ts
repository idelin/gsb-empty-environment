/**
 * 安全消息宿主：postMessage 接收端的来源授权。
 *
 * 反模式（浏览器中常见的漏洞写法）是拿 event.origin 字符串做比较；一旦
 * 对端是沙箱文档，event.origin === "null"，而 "null" 与任何不透明来源的
 * 显示形式相同，字符串比较无法区分具体身份。
 *
 * 本模块要求发送端把结构化 {@link Origin} 对象随消息一并交付，接收端用
 * {@link sameOrigin}（或允许列表上的结构相等比较）做授权决策。序列化字符串
 * 只进入日志，不进入安全判断。
 */

import {
  sameOrigin,
  serializeOrigin,
  type Origin,
} from "./origin.ts";

export interface SecureMessage<T = unknown> {
  readonly data: T;
  /** 发送端的结构化来源；由传输层（模拟 postMessage）负责随消息交付。 */
  readonly senderOrigin: Origin;
}

export type AllowedOrigin = Origin;

export interface DeliveredMessage<T = unknown> extends SecureMessage<T> {
  /** 仅供显示/排障使用，授权逻辑不得读取该字段。 */
  readonly senderOriginDisplay: string;
}

export interface RejectedMessage<T = unknown> {
  readonly reason: "origin-mismatch";
  readonly senderOriginDisplay: string;
  readonly data: T;
}

export type ReceiveResult<T = unknown> =
  | { accepted: true; message: DeliveredMessage<T> }
  | { accepted: false; rejection: RejectedMessage<T> };

/**
 * 判断来源是否命中允许列表。
 *
 * 纯结构化比较：不透明来源只有身份令牌完全相同才命中，两个各自独立的
 * 不透明来源即使显示都是 "null" 也不会互相命中。
 */
export function isOriginAllowed(sender: Origin, allowed: readonly AllowedOrigin[]): boolean {
  return allowed.some((trusted) => sameOrigin(sender, trusted));
}

/**
 * 安全消息宿主：持有允许来源列表，按结构化来源决定收或拒。
 *
 * 注意允许列表中可以登记不透明来源（例如同一沙箱继承下来的身份），
 * 登记后只有持有同一身份的消息才能通过。
 */
export class SecureMessageHost<T = unknown> {
  readonly #allowed: AllowedOrigin[];
  readonly #accepted: DeliveredMessage<T>[] = [];
  readonly #rejected: RejectedMessage<T>[] = [];

  constructor(allowedOrigins: readonly AllowedOrigin[]) {
    this.#allowed = [...allowedOrigins];
  }

  get acceptedMessages(): readonly DeliveredMessage<T>[] {
    return this.#accepted;
  }

  get rejectedMessages(): readonly RejectedMessage<T>[] {
    return this.#rejected;
  }

  /** 模拟 message 事件入口：来源校验通过才投递，否则拒绝并留痕。 */
  receive(message: SecureMessage<T>): ReceiveResult<T> {
    const senderOriginDisplay = serializeOrigin(message.senderOrigin);
    if (!isOriginAllowed(message.senderOrigin, this.#allowed)) {
      const rejection: RejectedMessage<T> = {
        reason: "origin-mismatch",
        senderOriginDisplay,
        data: message.data,
      };
      this.#rejected.push(rejection);
      return { accepted: false, rejection };
    }
    const delivered: DeliveredMessage<T> = { ...message, senderOriginDisplay };
    this.#accepted.push(delivered);
    return { accepted: true, message: delivered };
  }
}
