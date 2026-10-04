/**
 * 结果不可变策略。
 *
 * 缓存对外承诺：调用方拿到结果后无论怎么改，都不会污染缓存快照，
 * 也不会影响其他调用方。默认策略分两类：
 *
 * - TypedArray 结果：无法冻结（V8 拒绝冻结非空 TypedArray），
 *   因此入库时保存私有副本（snapshot），每次发放再复制一份（release）；
 * - 普通对象/数组结果：入库时递归深冻结，之后共享同一冻结对象。
 *
 * 结果里嵌套 TypedArray 的普通对象默认策略搞不定，会抛出明确错误，
 * 此时应通过 ResultCacheOptions.immutability 提供自定义策略。
 */
export interface ImmutabilityStrategy<R> {
  /** 计算产出 -> 缓存私有快照。 */
  snapshot(value: R): R;
  /** 缓存快照 -> 发放给某个调用方的值。 */
  release(stored: R): R;
}

function isTypedArray(value: unknown): value is { slice(): unknown } {
  return ArrayBuffer.isView(value) && !(value instanceof DataView);
}

function deepFreeze<T>(value: T): T {
  const seen = new Set<object>();
  const walk = (v: unknown): void => {
    if (v === null || typeof v !== 'object') return;
    if (ArrayBuffer.isView(v)) {
      throw new TypeError(
        'result contains a (nested) typed array; provide a custom immutability strategy',
      );
    }
    const obj = v as Record<string, unknown>;
    if (seen.has(obj)) return;
    seen.add(obj);
    for (const key of Object.keys(obj)) {
      walk(obj[key]);
    }
    Object.freeze(obj);
  };
  walk(value);
  return value;
}

export function defaultImmutability<R>(): ImmutabilityStrategy<R> {
  return {
    snapshot(value: R): R {
      if (isTypedArray(value)) {
        return value.slice() as R;
      }
      return deepFreeze(value);
    },
    release(stored: R): R {
      if (isTypedArray(stored)) {
        return stored.slice() as R;
      }
      return stored;
    },
  };
}
