/**
 * 主机侧可信 postMessage 接收组件的抽象类型。
 *
 * 安全模型概述：
 *  - 每条消息在**送达时刻**同步抓取来源（origin）与发送者当前文档代次
 *    （document generation），组成不可变的可信元数据 `TrustedSender`。
 *  - "回复能力"（reply capability）由 `(窗口对象, origin, 文档代次)` 三元组
 *    唯一确定。窗口在导航后仍为同一个 WindowProxy，但文档代次变化，
 *    旧能力立即失效，且不能被新文档继承。
 */

/**
 * 对 WindowProxy 的最小结构化描述。组件不直接依赖 DOM 类型，
 * 真实宿主中它就是 window / iframe.contentWindow / event.source。
 * 运行时通过 {@link WINDOW_BRAND} 鸭子类型识别，避免跨 realm 下 instanceof 失效。
 */
export interface WindowProxyLike {
  /** 内部品牌标记，见 {@link WINDOW_BRAND}。 */
  readonly [WINDOW_BRAND]: true;
  /** 调试用标识，不参与安全判定。 */
  readonly id: string;
}

/** WindowProxyLike 的品牌字段名（字符串键以兼容 Node type-stripping）。 */
export const WINDOW_BRAND = "__trustedWindowProxy";

/**
 * 一次导航事件。必须在窗口实际切换文档**之后**派发，
 * 此时 `window.document` 已经是新文档。
 */
export interface NavigationEvent {
  /** 发生导航的窗口（复用的 WindowProxy）。 */
  readonly window: WindowProxyLike;
  /** 导航前的文档代次；首个文档为 0，首次导航前为 0。 */
  readonly previousGeneration: number;
  /** 导航后的文档代次，严格递增。 */
  readonly generation: number;
  /** 新文档的来源。 */
  readonly origin: string;
}

/** 订阅导航事件；返回取消订阅函数。 */
export type NavigationObserver = (
  listener: (event: NavigationEvent) => void,
) => () => void;

/**
 * 传输层投递的**原始消息事件**。
 *
 * 关键约定：`origin` 与 `generation` 是平台/传输层在消息**发送时刻**
 * 就固化并随事件一起携带的可信数据，而不是回复时再去读取窗口当前状态——
 * 那时窗口可能已经导航。真实浏览器里 origin 由浏览器保证；
 * document generation 由宿主的导航记账（navigation observer 维护的
 * 单调计数器）提供，JS 无法伪造或重写。
 */
export interface RawMessageEvent<T = unknown> {
  /** 应用负载，未经校验，不可信。 */
  readonly data: T;
  /** 发送时刻的来源（浏览器保证，页面无法伪造）。 */
  readonly origin: string;
  /** 发送时刻发送窗口所处的文档代次。 */
  readonly generation: number;
  /** 发送窗口（导航后会被复用的 WindowProxy）。 */
  readonly source: WindowProxyLike;
}

/** 传输层抽象：投递原始消息、按目标来源发送回复。 */
export interface MessageTransport {
  /** 订阅原始消息事件；返回取消订阅函数。 */
  listen(handler: (event: RawMessageEvent) => void): () => void;
  /**
   * 向指定窗口的**当前文档**投递一条消息。
   * @param targetOrigin 仅当目标当前文档来源匹配时才投递（"*" 表示不限制）。
   * @returns 是否被传输层接受投递。
   */
  post(
    target: WindowProxyLike,
    message: unknown,
    targetOrigin: string,
  ): boolean;
}

/**
 * 不可变可信元数据 + 回复句柄。
 *
 * 它是一条消息专属的"能力"：
 *  - 字段全部 readonly，应用代码无法篡改 origin/generation。
 *  - 能力与具体文档代次绑定；同窗口导航后代次不符即失效。
 */
export interface TrustedRequest<T = unknown> {
  /** 经过 allowlist 校验的来源（发送时刻固化）。 */
  readonly origin: string;
  /** 发送时刻的文档代次（发送时刻固化）。 */
  readonly generation: number;
  /** 发送窗口（复用对象，本身不承载任何能力）。 */
  readonly source: WindowProxyLike;
  /** 未校验的应用负载。 */
  readonly data: T;
  /**
   * 仅向**发送该消息的那个文档**回复。
   * 若窗口已导航到新文档，调用会被拒绝且不产生任何外发数据。
   */
  reply(message: unknown): ReplyOutcome;
}

/** 回复结果，便于宿主审计与测试断言。 */
export type ReplyOutcome =
  | { readonly ok: true; readonly deliveredToGeneration: number }
  | {
      readonly ok: false;
      readonly reason:
        | "stale-generation"
        | "window-navigated"
        | "capability-revoked"
        | "capability-consumed"
        | "transport-rejected";
    };

/** 应用层消息处理器；可异步执行（例如先 await 校验再回复）。 */
export type MessageHandler<T = unknown> = (
  request: TrustedRequest<T>,
) => void | Promise<void>;

/** 接收组件的可观测统计，用于测试与安全审计。 */
export interface ReceiverStats {
  /** 传输层投递的原始消息总数。 */
  readonly received: number;
  /** 来源不在 allowlist 而被拒绝的数量。 */
  readonly rejectedOrigin: number;
  /** 送达时文档代次已过期（消息所属文档已不在）的数量。 */
  readonly rejectedStale: number;
  /** 成功交给应用处理器的数量。 */
  readonly accepted: number;
  /** 因导航而被撤销的能力数量。 */
  readonly revokedByNavigation: number;
  /** 成功发出的回复数量。 */
  readonly repliesDelivered: number;
  /** 被拒绝的回复数量（含导航后回复、重复回复等）。 */
  readonly repliesDenied: number;
}
