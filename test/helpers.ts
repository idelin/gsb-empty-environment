import { MemoryBackend, type KvBackend, type MinimalWebStorage } from "../src/backends.ts";
import type { PromptContext, PromptHandler } from "../src/storage-access.ts";

export const SITE_A = "https://news.example";
export const SITE_B = "https://shop.example";
export const EMBED_X = "https://widget.example";
export const EMBED_Y = "https://ads.example";
export const NAMESPACE = "user-vault";
export const KEY = "token";

/**
 * 带“闸门”的后端：set 进入后会阻塞在 gate 上，测试可在此时插入
 * 导航/撤销，从而确定性地构造“旧事务”。
 */
export class GateBackend implements KvBackend {
  readonly delegate = new MemoryBackend();
  #blocked: (() => void) | null = null;
  #gate: Promise<void> | null = null;
  #entered: (() => void) | null = null;
  #enteredSignal: Promise<void> | null = null;

  arm(): void {
    this.#gate = new Promise<void>((resolve) => {
      this.#blocked = resolve;
    });
    this.#enteredSignal = new Promise<void>((resolve) => {
      this.#entered = resolve;
    });
  }

  /** 等待下一个被闸门拦截的 set 真正进入阻塞点。 */
  waitUntilBlocked(): Promise<void> {
    return this.#enteredSignal ?? Promise.resolve();
  }

  release(): void {
    const resolve = this.#blocked;
    this.#blocked = null;
    this.#gate = null;
    resolve?.();
  }

  async get(key: string): Promise<string | null> {
    return this.delegate.get(key);
  }

  async set(key: string, value: string): Promise<void> {
    if (this.#gate) {
      this.#entered?.();
      this.#entered = null;
      await this.#gate;
    }
    this.delegate.set(key, value);
  }

  async remove(key: string): Promise<void> {
    this.delegate.remove(key);
  }

  async has(key: string): Promise<boolean> {
    return this.delegate.has(key);
  }
}

/** 记录每次用户提示的受控处理器。 */
export function countingPrompt(allow = true): {
  handler: PromptHandler;
  calls: PromptContext[];
} {
  const calls: PromptContext[] = [];
  return {
    calls,
    handler: (context) => {
      calls.push(context);
      return allow;
    },
  };
}

/** 非浏览器环境下的最小 Web Storage 伪造实现。 */
export class FakeWebStorage implements MinimalWebStorage {
  readonly #store = new Map<string, string>();

  get length(): number {
    return this.#store.size;
  }

  getItem(key: string): string | null {
    return this.#store.has(key) ? this.#store.get(key)! : null;
  }

  setItem(key: string, value: string): void {
    this.#store.set(key, String(value));
  }

  removeItem(key: string): void {
    this.#store.delete(key);
  }

  clear(): void {
    this.#store.clear();
  }

  key(index: number): string | null {
    return [...this.#store.keys()][index] ?? null;
  }
}
