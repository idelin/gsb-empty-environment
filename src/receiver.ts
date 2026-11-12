import type {
  MessageHandler,
  MessageTransport,
  NavigationEvent,
  NavigationObserver,
  RawMessageEvent,
  ReceiverStats,
  ReplyOutcome,
  TrustedRequest,
  WindowProxyLike,
} from "./types.ts";
import { WINDOW_BRAND } from "./types.ts";

/** 单条消息的内部能力记录，外部只能拿到不可变的 TrustedRequest 视图。 */
interface Capability {
  readonly window: WindowProxyLike;
  readonly origin: string;
  readonly generation: number;
  /** 导航撤销后置为失效。 */
  revoked: boolean;
  /** reply 一次性消费标记，防止同一能力被重放。 */
  consumed: boolean;
}

/** 每个窗口当前存活的文档代次（由导航事件单调推进）。 */
interface WindowState {
  generation: number;
  origin: string;
}

/**
 * 主机侧可信消息接收器。
 *
 * 竞争条件说明：`postMessage` 事件回调通过 origin allowlist 校验之后，
 * 应用往往还要执行异步工作（IO、解密、签名校验……）。在 await 期间
 * 发送窗口可能已经导航，WindowProxy 被新文档复用。此时若直接用缓存的
 * `event.source.postMessage(...)` 回复，数据会发给新文档——即便浏览器的
 * targetOrigin 检查也挡不住"同源导航"的场景。
 *
 * 本组件把 (source, origin, generation) 作为不可变可信元数据固化下来，
 * 回复前强制校验窗口当前代次仍等于发送时代次，并在导航事件到达时
 * 主动撤销旧文档的全部能力，从而保证响应只对原文档有效。
 */
export class TrustedMessageReceiver<T = unknown> {
  readonly #transport: MessageTransport;
  readonly #observeNavigation: NavigationObserver;
  readonly #allowedOrigins: ReadonlySet<string>;
  readonly #handler: MessageHandler<T>;

  /** window.id -> 当前窗口状态。 */
  readonly #windowStates = new Map<string, WindowState>();
  /** 能力 id -> 能力记录。 */
  readonly #capabilities = new Map<number, Capability>();
  /** 每个窗口持有的能力 id，导航时批量撤销。 */
  readonly #capabilitiesByWindow = new Map<string, Set<number>>();

  #nextCapabilityId = 1;
  #disposed = false;
  #unlistenTransport: (() => void) | null = null;
  #unobserveNavigation: (() => void) | null = null;

  #stats = {
    received: 0,
    rejectedOrigin: 0,
    rejectedStale: 0,
    accepted: 0,
    revokedByNavigation: 0,
    repliesDelivered: 0,
    repliesDenied: 0,
  };

  constructor(options: {
    transport: MessageTransport;
    observeNavigation: NavigationObserver;
    /** 允许接收消息的来源白名单，"*" 不会被接受——必须显式列举。 */
    allowedOrigins: readonly string[];
    handler: MessageHandler<T>;
  }) {
    this.#transport = options.transport;
    this.#observeNavigation = options.observeNavigation;
    this.#handler = options.handler;

    if (options.allowedOrigins.length === 0) {
      throw new Error("allowedOrigins must not be empty");
    }
    if (options.allowedOrigins.some((o) => o === "*" || o === "null")) {
      throw new Error('wildcard "*" and opaque "null" origins are not allowed');
    }
    this.#allowedOrigins = new Set(options.allowedOrigins);

    this.#unlistenTransport = this.#transport.listen((event) => {
      this.#dispatch(event);
    });
    this.#unobserveNavigation = this.#observeNavigation((event) => {
      this.#onNavigation(event);
    });
  }

  get stats(): Readonly<ReceiverStats> {
    return { ...this.#stats };
  }

  /** 停止订阅传输层与导航事件；之后所有能力一律拒绝回复。 */
  dispose(): void {
    if (this.#disposed) return;
    this.#disposed = true;
    this.#unlistenTransport?.();
    this.#unobserveNavigation?.();
    for (const cap of this.#capabilities.values()) {
      cap.revoked = true;
    }
    this.#capabilities.clear();
    this.#capabilitiesByWindow.clear();
  }

  /**
   * 原始消息入口。所有可信元数据在此**同步**抓取，
   * 在任何 await 之前完成固化，避免 await 期间窗口导航导致的 TOCTOU。
   */
  #dispatch(event: RawMessageEvent): void {
    if (this.#disposed) return;
    this.#stats.received++;

    const source = event.source;
    if (!source || source[WINDOW_BRAND] !== true) {
      // 没有可识别的发送窗口就无法绑定能力，直接丢弃。
      this.#stats.rejectedStale++;
      return;
    }

    // 同步快照：来源与代次取自随事件携带的平台可信字段。
    const origin = event.origin;
    const generation = event.generation;

    // 1) 来源校验（allowlist）。
    if (!this.#allowedOrigins.has(origin)) {
      this.#stats.rejectedOrigin++;
      return;
    }

    // 2) 活性校验：消息送达时，该窗口当前文档必须仍是发送时的文档。
    const state = this.#windowStates.get(source.id);
    if (state && state.generation !== generation) {
      this.#stats.rejectedStale++;
      return;
    }
    if (!state) {
      // 首次见到该窗口：以事件携带的可信代次建立基线。
      this.#windowStates.set(source.id, { generation, origin });
    }

    // 3) 固化为不可变能力。
    const capabilityId = this.#nextCapabilityId++;
    const capability: Capability = {
      window: source,
      origin,
      generation,
      revoked: false,
      consumed: false,
    };
    this.#capabilities.set(capabilityId, capability);
    let owned = this.#capabilitiesByWindow.get(source.id);
    if (!owned) {
      owned = new Set();
      this.#capabilitiesByWindow.set(source.id, owned);
    }
    owned.add(capabilityId);

    this.#stats.accepted++;

    const request: TrustedRequest<T> = {
      origin,
      generation,
      source,
      data: event.data as T,
      reply: (message: unknown): ReplyOutcome =>
        this.#reply(capabilityId, message),
    };

    // 处理器可以是异步的；其抛出不影响组件状态。
    Promise.resolve()
      .then(() => this.#handler(request))
      .catch((error: unknown) => {
        // 宿主错误不应导致能力被悄悄复用；仅记录并吞掉。
        console.error("TrustedMessageReceiver handler error:", error);
      });
  }

  /** 导航事件：推进窗口代次并撤销旧文档的全部能力。 */
  #onNavigation(event: NavigationEvent): void {
    if (this.#disposed) return;
    const previous = this.#windowStates.get(event.window.id);
    this.#windowStates.set(event.window.id, {
      generation: event.generation,
      origin: event.origin,
    });

    // 防御性断言：代次必须单调递增。
    if (previous && event.generation <= previous.generation) {
      throw new Error(
        `navigation generation must be monotonic for window ${event.window.id}`,
      );
    }

    const owned = this.#capabilitiesByWindow.get(event.window.id);
    if (!owned) return;
    for (const id of owned) {
      const cap = this.#capabilities.get(id);
      // 该窗口名下仍存活的能力都属于被替换掉的旧文档，统一撤销。
      if (cap && !cap.revoked && cap.generation <= event.previousGeneration) {
        cap.revoked = true;
        this.#stats.revokedByNavigation++;
      }
    }
    owned.clear();
  }

  /** 能力回复：三道闸门，任何一道不过都不外发数据。 */
  #reply(capabilityId: number, message: unknown): ReplyOutcome {
    if (this.#disposed) {
      this.#stats.repliesDenied++;
      return { ok: false, reason: "capability-revoked" };
    }

    const cap = this.#capabilities.get(capabilityId);
    if (!cap) {
      this.#stats.repliesDenied++;
      return { ok: false, reason: "capability-revoked" };
    }

    // 活性闸门优先：窗口当前文档必须仍是发送时的那一代。
    // 这是防止"窗口对象复用把响应泄漏给新文档"的核心检查。
    const state = this.#windowStates.get(cap.window.id);
    if (!state || state.generation !== cap.generation) {
      cap.revoked = true;
      this.#stats.repliesDenied++;
      return { ok: false, reason: "window-navigated" };
    }
    // 来源同样必须未变（防御同源/跨源导航的各类组合）。
    if (state.origin !== cap.origin) {
      cap.revoked = true;
      this.#stats.repliesDenied++;
      return { ok: false, reason: "stale-generation" };
    }

    // 活性通过后再检查撤销/消费标记（dispose 等场景）。
    if (cap.revoked) {
      this.#stats.repliesDenied++;
      return { ok: false, reason: "capability-revoked" };
    }
    if (cap.consumed) {
      this.#stats.repliesDenied++;
      return { ok: false, reason: "capability-consumed" };
    }

    // 显式 targetOrigin = 能力固化的来源，绝不使用 "*"。
    const accepted = this.#transport.post(cap.window, message, cap.origin);
    if (!accepted) {
      this.#stats.repliesDenied++;
      return { ok: false, reason: "transport-rejected" };
    }

    cap.consumed = true;
    this.#stats.repliesDelivered++;
    return { ok: true, deliveredToGeneration: cap.generation };
  }
}
