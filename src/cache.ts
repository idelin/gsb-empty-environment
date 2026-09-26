import { digest } from './digest.ts';
import type { ValidationResult } from './validator.ts';

export interface CacheStats {
  hits: number;
  misses: number;
}

export interface CacheEntry {
  result: ValidationResult;
  cacheKey: string;
  schemaDigest: string;
  sampleDigest: string;
  fromCache: boolean;
}

/**
 * 校验结果缓存。
 *
 * 缓存键 = sha256(规范化后的 schema) + '#' + sha256(规范化后的样本)。
 * schema 或样本任一字节内容变化都会得到不同的键，因此：
 *   - 样本改变后不能复用旧的“通过”结果，必须重新校验；
 *   - schema 改变后也不能复用旧结果。
 */
export class ValidationCache {
  private readonly store = new Map<string, ValidationResult>();

  readonly stats: CacheStats = { hits: 0, misses: 0 };

  private readonly compute: (schema: unknown, sample: unknown) => ValidationResult;

  constructor(compute: (schema: unknown, sample: unknown) => ValidationResult) {
    this.compute = compute;
  }

  static keyFor(schema: unknown, sample: unknown): {
    cacheKey: string;
    schemaDigest: string;
    sampleDigest: string;
  } {
    const schemaDigest = digest(schema);
    const sampleDigest = digest(sample);
    return { cacheKey: `${schemaDigest}#${sampleDigest}`, schemaDigest, sampleDigest };
  }

  validate(schema: unknown, sample: unknown): CacheEntry {
    const { cacheKey, schemaDigest, sampleDigest } = ValidationCache.keyFor(schema, sample);
    const cached = this.store.get(cacheKey);
    if (cached !== undefined) {
      this.stats.hits += 1;
      return { result: cached, cacheKey, schemaDigest, sampleDigest, fromCache: true };
    }
    this.stats.misses += 1;
    const result = this.compute(schema, sample);
    this.store.set(cacheKey, result);
    return { result, cacheKey, schemaDigest, sampleDigest, fromCache: false };
  }
}
