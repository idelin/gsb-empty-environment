/**
 * 可替换的键值后端抽象。
 *
 * 组件本身不假设浏览器全局对象是否存在：分区缓存与未分区存储都通过这个
 * 接口访问，测试与 Node 环境可以注入内存实现，浏览器环境可注入
 * localStorage / sessionStorage 适配器。
 */
export interface KvBackend {
  get(key: string): string | null | Promise<string | null>;
  set(key: string, value: string): void | Promise<void>;
  remove(key: string): void | Promise<void>;
  has(key: string): boolean | Promise<boolean>;
}

/** 基于 Map 的内存后端，用于测试和默认装配。 */
export class MemoryBackend implements KvBackend {
  readonly #store = new Map<string, string>();

  get(key: string): string | null {
    return this.#store.has(key) ? this.#store.get(key)! : null;
  }

  set(key: string, value: string): void {
    this.#store.set(key, value);
  }

  remove(key: string): void {
    this.#store.delete(key);
  }

  has(key: string): boolean {
    return this.#store.has(key);
  }

  get size(): number {
    return this.#store.size;
  }
}

/**
 * 浏览器 Storage（localStorage/sessionStorage）适配器。
 * 仅在调用点延迟引用传入对象，构造期不触碰任何浏览器全局变量。
 */
export class WebStorageBackend implements KvBackend {
  readonly #storage: Storage;

  constructor(storage: Storage) {
    this.#storage = storage;
  }

  get(key: string): string | null {
    return this.#storage.getItem(key);
  }

  set(key: string, value: string): void {
    this.#storage.setItem(key, value);
  }

  remove(key: string): void {
    this.#storage.removeItem(key);
  }

  has(key: string): boolean {
    // 与 get 语义对齐：键存在但值为 null 时按不存在处理。
    return this.#storage.getItem(key) !== null;
  }
}

/** 最小 Web Storage 形状，便于在非浏览器环境伪造 localStorage。 */
export interface MinimalWebStorage {
  getItem(key: string): string | null;
  setItem(key: string, value: string): void;
  removeItem(key: string): void;
  clear(): void;
  key(index: number): string | null;
  readonly length: number;
}

/** 把任意符合最小形状的对象包装成 KvBackend。 */
export class MinimalWebStorageBackend implements KvBackend {
  readonly #storage: MinimalWebStorage;

  constructor(storage: MinimalWebStorage) {
    this.#storage = storage;
  }

  get(key: string): string | null {
    return this.#storage.getItem(key);
  }

  set(key: string, value: string): void {
    this.#storage.setItem(key, value);
  }

  remove(key: string): void {
    this.#storage.removeItem(key);
  }

  has(key: string): boolean {
    return this.#storage.getItem(key) !== null;
  }
}
