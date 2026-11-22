import { randomBytes, scrypt, timingSafeEqual } from 'node:crypto';
import { promisify } from 'node:util';

const scryptAsync = promisify(scrypt);

/** 密码哈希器接口，便于测试中替换为快速实现。 */
export interface PasswordHasher {
  hash(password: string): Promise<string>;
  verify(password: string, stored: string): Promise<boolean>;
}

/**
 * 基于 scrypt 的哈希器。
 * 存储格式：scrypt$N$r$p$saltHex$hashHex
 */
export class ScryptHasher implements PasswordHasher {
  private readonly keylen: number;

  constructor(
    private readonly cost = 16384,
    private readonly blockSize = 8,
    private readonly parallelization = 1,
    keylen = 32,
  ) {
    this.keylen = keylen;
  }

  async hash(password: string): Promise<string> {
    const salt = randomBytes(16);
    const derived = (await scryptAsync(password, salt, this.keylen, {
      N: this.cost,
      r: this.blockSize,
      p: this.parallelization,
    })) as Buffer;
    return [
      'scrypt',
      this.cost,
      this.blockSize,
      this.parallelization,
      salt.toString('hex'),
      derived.toString('hex'),
    ].join('$');
  }

  async verify(password: string, stored: string): Promise<boolean> {
    const parts = stored.split('$');
    if (parts.length !== 6 || parts[0] !== 'scrypt') {
      return false;
    }
    const [, n, r, p, saltHex, hashHex] = parts;
    const expected = Buffer.from(hashHex, 'hex');
    const derived = (await scryptAsync(password, Buffer.from(saltHex, 'hex'), expected.length, {
      N: Number(n),
      r: Number(r),
      p: Number(p),
    })) as Buffer;
    if (derived.length !== expected.length) {
      return false;
    }
    return timingSafeEqual(derived, expected);
  }
}

/**
 * 快速哈希器：仅用于测试，避免真实 scrypt 的耗时让用例变慢。
 * 仍是确定性映射，保证 verify 语义正确。
 */
export class FastHasher implements PasswordHasher {
  private encode(password: string): string {
    return `fast$${Buffer.from(password).toString('base64')}`;
  }

  async hash(password: string): Promise<string> {
    return this.encode(password);
  }

  async verify(password: string, stored: string): Promise<boolean> {
    const candidate = Buffer.from(this.encode(password));
    const expected = Buffer.from(stored);
    if (candidate.length !== expected.length) {
      return false;
    }
    return timingSafeEqual(candidate, expected);
  }
}
