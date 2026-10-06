import { evaluateCondition, type Expr } from './expression.ts';

/**
 * 上下文键值服务。每次实际变更都会使 version 单调递增；
 * 列表快照记录当时的 version，执行时据此判断是否已过期。
 */
export class ContextKeyService {
  #values = new Map<string, unknown>();
  #version = 0;

  get version(): number {
    return this.#version;
  }

  get(key: string): unknown {
    return this.#values.get(key);
  }

  /** 返回是否发生了实际变更（值相同则不 bump 版本）。 */
  set(key: string, value: unknown): boolean {
    if (this.#values.has(key) && this.#values.get(key) === value) {
      return false;
    }
    this.#values.set(key, value);
    this.#version++;
    return true;
  }

  unset(key: string): boolean {
    if (!this.#values.delete(key)) {
      return false;
    }
    this.#version++;
    return true;
  }

  evaluate(expr: Expr): boolean {
    return evaluateCondition(expr, (key) => this.get(key));
  }

  snapshot(): Record<string, unknown> {
    return Object.fromEntries(this.#values);
  }
}
