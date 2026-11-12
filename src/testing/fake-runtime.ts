import type {
  MessageChannelFactory,
  MessageChannelLike,
  MessageHandler,
  PortLike,
  SharedWorkerLike,
  WorkerFactory,
} from '../protocol.ts';
import type { TenantWorkerAgent } from '../worker-agent.ts';

interface QueuedEvent {
  data: unknown;
  ports: PortLike[];
}

/** 纯内存 MessagePort，双向配对，异步派发，语义贴近浏览器实现。 */
export class FakeMessagePort implements PortLike {
  private remote: FakeMessagePort | undefined;
  private readonly listeners = new Set<MessageHandler>();
  private readonly queue: QueuedEvent[] = [];
  private started = false;
  private closed = false;

  static pair(): [FakeMessagePort, FakeMessagePort] {
    const left = new FakeMessagePort();
    const right = new FakeMessagePort();
    left.remote = right;
    right.remote = left;
    return [left, right];
  }

  addEventListener(type: string, listener: MessageHandler): void {
    if (type === 'message') {
      this.listeners.add(listener);
    }
  }

  removeEventListener(type: string, listener: MessageHandler): void {
    if (type === 'message') {
      this.listeners.delete(listener);
    }
  }

  start(): void {
    this.started = true;
    this.flush();
  }

  close(): void {
    this.flush();
    this.closed = true;
    this.queue.length = 0;
  }

  /** 等待已入队的消息完成微任务派发，测试用于消除时序竞态。 */
  async settle(): Promise<void> {
    for (let spin = 0; spin < 10 && this.queue.length > 0; spin += 1) {
      await Promise.resolve();
    }
  }

  postMessage(message: unknown, transfer: unknown[] = []): void {
    if (this.closed) {
      throw new Error('FakeMessagePort is closed');
    }
    if (this.remote === undefined || this.remote.closed) {
      return;
    }
    const ports: PortLike[] = [];
    for (const candidate of transfer) {
      if (isFakePort(candidate)) {
        ports.push(candidate);
      }
    }
    this.remote.deliver({ data: message, ports });
  }

  private deliver(event: QueuedEvent): void {
    if (this.closed) {
      return;
    }
    this.queue.push(event);
    if (this.started) {
      queueMicrotask(() => this.flush());
    }
  }

  private flush(): void {
    if (!this.started || this.closed) {
      return;
    }
    while (this.queue.length > 0) {
      const event = this.queue.shift();
      if (event === undefined) {
        break;
      }
      for (const listener of this.listeners) {
        listener(event);
      }
    }
  }
}

function isFakePort(value: unknown): value is FakeMessagePort {
  return value instanceof FakeMessagePort;
}

export class FakeMessageChannel implements MessageChannelLike {
  readonly port1: FakeMessagePort;
  readonly port2: FakeMessagePort;

  constructor() {
    const [port1, port2] = FakeMessagePort.pair();
    this.port1 = port1;
    this.port2 = port2;
  }
}

export const fakeMessageChannelFactory: MessageChannelFactory = () =>
  new FakeMessageChannel();

interface Registration {
  scriptUrl: string;
}

export interface FakeRuntimeOptions {
  scriptUrl: string;
  agent: TenantWorkerAgent;
}

/**
 * 模拟同源页面的 SharedWorker 注册表：
 * 同名 worker 被复用，同名但脚本 URL 不同直接抛错（与浏览器构造行为一致）。
 */
export class FakeSharedWorkerRuntime {
  private readonly registry = new Map<string, Registration>();
  private readonly options: FakeRuntimeOptions;

  constructor(options: FakeRuntimeOptions) {
    this.options = options;
  }

  readonly createWorker: WorkerFactory = (
    scriptUrl: string,
    name: string,
  ): SharedWorkerLike => {
    const existing = this.registry.get(name);
    if (existing !== undefined && existing.scriptUrl !== scriptUrl) {
      throw new Error(
        `SharedWorker name "${name}" is already bound to ${existing.scriptUrl}`,
      );
    }
    if (existing === undefined) {
      this.registry.set(name, { scriptUrl });
    }

    const [pagePort, workerPort] = FakeMessagePort.pair();
    this.options.agent.handleConnectPort(workerPort);
    return { port: pagePort };
  };

  hasName(name: string): boolean {
    return this.registry.has(name);
  }

  registeredNames(): string[] {
    return [...this.registry.keys()];
  }
}
