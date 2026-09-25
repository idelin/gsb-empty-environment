import type { ModuleInstance } from "./linker.ts";

/** 模块缓存：以完整依赖版本向量隔离，绝不跨版本混用。 */
export class ModuleCache {
  private readonly store = new Map<string, Map<string, unknown>>();

  /** 缓存键 = 模块的完整依赖版本向量（含自身及全部传递依赖的版本）。 */
  static keyOf(instance: ModuleInstance): string {
    return instance.versionVector;
  }

  private bucket(instance: ModuleInstance, create: boolean): Map<string, unknown> | undefined {
    const key = ModuleCache.keyOf(instance);
    let b = this.store.get(key);
    if (!b && create) {
      b = new Map();
      this.store.set(key, b);
    }
    return b;
  }

  get(instance: ModuleInstance, prop: string): unknown {
    return this.bucket(instance, false)?.get(prop);
  }

  has(instance: ModuleInstance, prop: string): boolean {
    return this.bucket(instance, false)?.has(prop) ?? false;
  }

  set(instance: ModuleInstance, prop: string, value: unknown): void {
    (this.bucket(instance, true) as Map<string, unknown>).set(prop, value);
  }
}
