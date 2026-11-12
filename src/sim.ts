/**
 * 可控的最小浏览器环境模拟。
 *
 * 目的不是完整实现 HTML 规范，而是确定性地复现关键安全语义：
 *  1. WindowProxy 在导航中被复用（同一个 JS 对象，内部文档切换）。
 *  2. postMessage 是异步的，且消息携带"发送时刻"固化的 origin 与
 *     文档代次；导航后旧消息的可信元数据不随新文档改变。
 *  3. targetOrigin 不匹配时浏览器静默丢弃（不抛异常）。
 *  4. 导航事件可由测试精确控制时机，从而构造"接收→异步校验→导航→回复"
 *     的交错序列。
 */

import type {
  MessageTransport,
  NavigationEvent,
  NavigationObserver,
  RawMessageEvent,
  WindowProxyLike,
} from "./types.ts";
import { WINDOW_BRAND } from "./types.ts";

/** FIFO 任务队列：所有异步动作（消息投递、导航、应用回调）都入队，测试可逐步排空。 */
export class TaskQueue {
  #tasks: Array<() => void> = [];

  enqueue(task: () => void): void {
    this.#tasks.push(task);
  }

  /** 执行当前队列中已有的任务；任务执行中新入队的任务也会被排空。 */
  async drain(): Promise<void> {
    while (this.#tasks.length > 0) {
      const task = this.#tasks.shift()!;
      task();
      // 让 Promise 微任务（async 处理器中的 await 链）有机会推进。
      await Promise.resolve();
    }
  }

  get pending(): number {
    return this.#tasks.length;
  }
}

/** 模拟一个可被导航复用的 WindowProxy。 */
export class SimWindow implements WindowProxyLike {
  readonly [WINDOW_BRAND]: true = true;
  readonly id: string;
  #origin: string;
  #generation = 0;
  #navListeners = new Set<(event: NavigationEvent) => void>();
  /** 当前文档收到的消息（按文档代次分桶，导航后旧文档桶不再增长）。 */
  readonly mailboxes = new Map<number, unknown[]>();
  /** 被 targetOrigin 检查丢弃的消息计数。 */
  droppedByTargetOrigin = 0;

  constructor(id: string, initialOrigin: string) {
    this.id = id;
    this.#origin = initialOrigin;
    this.mailboxes.set(0, []);
  }

  get origin(): string {
    return this.#origin;
  }

  get generation(): number {
    return this.#generation;
  }

  /** 传输层调用：仅当 targetOrigin 匹配当前文档时才落入当前文档邮箱。 */
  accept(message: unknown, targetOrigin: string): boolean {
    if (targetOrigin !== "*" && targetOrigin !== this.#origin) {
      this.droppedByTargetOrigin++;
      return false;
    }
    const box = this.mailboxes.get(this.#generation);
    if (!box) throw new Error("mailbox missing for current generation");
    box.push(message);
    return true;
  }

  /** 当前文档邮箱快照（测试断言用）。 */
  currentMailbox(): unknown[] {
    return [...(this.mailboxes.get(this.#generation) ?? [])];
  }

  /** 取出某一代文档收到的消息（用于证明旧文档/新文档各自的内容边界）。 */
  mailboxForGeneration(generation: number): unknown[] {
    return [...(this.mailboxes.get(generation) ?? [])];
  }

  onNavigation(listener: (event: NavigationEvent) => void): () => void {
    this.#navListeners.add(listener);
    return () => this.#navListeners.delete(listener);
  }

  /**
   * 执行导航：复用同一个 SimWindow 实例，切换内部文档，
   * 代次单调递增，并同步派发导航事件（文档已切换之后）。
   */
  navigate(nextOrigin: string): NavigationEvent {
    const previousGeneration = this.#generation;
    this.#generation++;
    this.#origin = nextOrigin;
    this.mailboxes.set(this.#generation, []);
    const event: NavigationEvent = {
      window: this,
      previousGeneration,
      generation: this.#generation,
      origin: nextOrigin,
    };
    for (const listener of this.#navListeners) listener(event);
    return event;
  }
}

/** 导航观察器：汇聚一个或多个窗口的导航事件。 */
export class SimNavigationObserver {
  readonly #listeners = new Set<(event: NavigationEvent) => void>();

  observe: NavigationObserver = (listener) => {
    this.#listeners.add(listener);
    return () => this.#listeners.delete(listener);
  };

  watch(...windows: SimWindow[]): void {
    for (const win of windows) {
      win.onNavigation((event) => {
        for (const listener of this.#listeners) listener(event);
      });
    }
  }

  emit(event: NavigationEvent): void {
    for (const listener of this.#listeners) listener(event);
  }
}

/**
 * 模拟 postMessage 总线。
 * `postMessage` 在**发送时刻**读取并固化发送窗口的 origin/generation，
 * 经任务队列异步投递给接收方；投递前发生的导航不会改写事件元数据。
 */
export class SimTransport implements MessageTransport {
  readonly #queue: TaskQueue;
  readonly #handlers = new Set<(event: RawMessageEvent) => void>();

  constructor(queue: TaskQueue) {
    this.#queue = queue;
  }

  listen(handler: (event: RawMessageEvent) => void): () => void {
    this.#handlers.add(handler);
    return () => this.#handlers.delete(handler);
  }

  /**
   * @param sender 发送窗口（其当前 origin/generation 被固化到事件）
   * @param target 接收窗口
   */
  postMessage(
    sender: SimWindow,
    target: SimWindow,
    data: unknown,
  ): void {
    // 发送时刻同步固化可信元数据。
    const event: RawMessageEvent = {
      data,
      origin: sender.origin,
      generation: sender.generation,
      source: sender,
    };
    this.#queue.enqueue(() => {
      for (const handler of this.#handlers) handler(event);
    });
  }

  /** 主机回复路径：异步投递给目标窗口的**当前**文档，交由 targetOrigin 把关。 */
  post(target: WindowProxyLike, message: unknown, targetOrigin: string): boolean {
    const win = target as SimWindow;
    this.#queue.enqueue(() => {
      win.accept(message, targetOrigin);
    });
    return true;
  }
}
