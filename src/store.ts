import type { PasswordHasher } from './hasher.ts';

/** 账号存储接口。 */
export interface UserStore {
  /** 返回存储的密码哈希；账号不存在时返回 null。 */
  getPasswordHash(account: string): Promise<string | null>;
}

/** 内存账号存储，便于演示与测试。 */
export class InMemoryUserStore implements UserStore {
  private readonly hashes = new Map<string, string>();

  constructor(private readonly hasher: PasswordHasher) {}

  /** 预置账号（在初始化阶段哈希密码）。 */
  async addUser(account: string, password: string): Promise<void> {
    this.hashes.set(account, await this.hasher.hash(password));
  }

  async getPasswordHash(account: string): Promise<string | null> {
    return this.hashes.get(account) ?? null;
  }
}
