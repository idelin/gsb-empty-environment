/**
 * 有限域枚举：按规模递增枚举模式下的代表值。
 *
 * 无限类型（string/number/无界 array）按预设的小字母表与长度窗口截断，
 * 因此枚举结果是有穷集合——足以覆盖枚举差集、null、字段缺省组合与
 * 数组长度边界这些判定所需的全部反例形态。主要用于测试交叉核对：
 * 对 checkCompatibility 的结论做穷举验证。
 */

import { arrayMaxLen, arrayMinLen } from "./schema.ts";
import type { Schema } from "./schema.ts";

export interface EnumerateOptions {
  /** 每个无限标量域最多取多少个代表值，默认 3 */
  readonly scalarSamples?: number;
  /** 数组长度在 [minLen, maxLen] 内最多再向上探多少，默认 2 */
  readonly extraArrayLengths?: number;
  /** 返回值的总量上限，默认 1000 */
  readonly limit?: number;
}

const STRING_SAMPLES = ["", "a", "b", "z", "ab"];
const NUMBER_SAMPLES = [0, 1, 2, -1, 42];

export function enumerateValues(schema: Schema, options: EnumerateOptions = {}): unknown[] {
  const scalarSamples = options.scalarSamples ?? 3;
  const extraArrayLengths = options.extraArrayLengths ?? 2;
  const limit = options.limit ?? 1000;
  const out: unknown[] = [];

  const emit = (v: unknown): boolean => {
    if (out.length >= limit) return false;
    out.push(v);
    return true;
  };

  const walk = (s: Schema): unknown[] => {
    switch (s.kind) {
      case "string":
        return STRING_SAMPLES.slice(0, scalarSamples);
      case "number":
        return NUMBER_SAMPLES.slice(0, scalarSamples);
      case "boolean":
        return [false, true];
      case "enum":
        return [...s.values];
      case "nullable":
        return [null, ...walk(s.inner)];
      case "object": {
        const entries = Object.entries(s.fields);
        let records: Record<string, unknown>[] = [{}];
        for (const [name, field] of entries) {
          const variants: Record<string, unknown>[] = [];
          const values = walk(field.schema);
          for (const rec of records) {
            if (!field.required) variants.push(rec); // 缺省
            for (const v of values) variants.push({ ...rec, [name]: v });
          }
          records = variants.slice(0, limit);
        }
        return records;
      }
      case "array": {
        const lo = arrayMinLen(s);
        const hi = Math.min(arrayMaxLen(s), lo + extraArrayLengths);
        const items = walk(s.items).slice(0, scalarSamples);
        const arrays: unknown[][] = [];
        for (let len = lo; len <= hi; len += 1) {
          // 笛卡尔积在 limit 内截断
          let tuples: unknown[][] = [[]];
          for (let i = 0; i < len; i += 1) {
            const next: unknown[][] = [];
            for (const tuple of tuples) {
              for (const item of items) {
                next.push([...tuple, item]);
                if (next.length >= limit) break;
              }
              if (next.length >= limit) break;
            }
            tuples = next;
          }
          arrays.push(...tuples);
        }
        return arrays;
      }
    }
  };

  for (const v of walk(schema)) {
    if (!emit(v)) break;
  }
  return out;
}
