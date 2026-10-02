import { StaleFenceTokenError } from "./errors.ts";

/**
 * 受 fencing token 保护的 KV 存储（模拟下游共享资源）。
 *
 * 记录所见过的最大 fencing token：携带更小或相等 token 的写请求
 * 一律拒绝，从而隔离“租约已过期但自己不知道”的僵尸持有者。
 */
export class FencedStore {
  private readonly data = new Map<string, string>();
  private highestFenceToken = 0;

  write(fenceToken: number, key: string, value: string): void {
    if (fenceToken <= this.highestFenceToken) {
      throw new StaleFenceTokenError(fenceToken, this.highestFenceToken);
    }
    this.highestFenceToken = fenceToken;
    this.data.set(key, value);
  }

  read(key: string): string | undefined {
    return this.data.get(key);
  }

  get currentFenceToken(): number {
    return this.highestFenceToken;
  }
}
