/**
 * nonce 存储抽象。生产环境实现为链上读取/提交：
 * checkAndIncrement 必须是原子的——仅当链上当前值等于 expected 时
 * 才递增并返回 true，否则返回 false，避免 TOCTOU 竞争。
 */
export interface NonceStore {
  /** 返回某签名者当前的链上 nonce。 */
  get(signer: string): Promise<bigint>;

  /**
   * 原子“比对并递增”。当前值等于 expected 时递增并返回 true，
   * 否则保持不变并返回 false。
   */
  checkAndIncrement(signer: string, expected: bigint): Promise<boolean>;
}

/**
 * 进程内内存实现，用于本地开发与测试。多实例/持久化场景需自行实现
 * NonceStore 对接链上状态。
 */
export class InMemoryNonceStore implements NonceStore {
  private readonly nonces = new Map<string, bigint>();

  async get(signer: string): Promise<bigint> {
    return this.nonces.get(signer.toLowerCase()) ?? 0n;
  }

  async checkAndIncrement(signer: string, expected: bigint): Promise<boolean> {
    const key = signer.toLowerCase();
    const current = this.nonces.get(key) ?? 0n;
    if (current !== expected) {
      return false;
    }
    this.nonces.set(key, current + 1n);
    return true;
  }
}
