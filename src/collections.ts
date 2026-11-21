import type { TaintRegistry, Val } from "./tval.ts";
import { TVal, previewValue } from "./tval.ts";

/**
 * 带污点标记的数组：元素各自携带自己的污点事实。
 * - 数组赋值/下标赋值：传播右值的污点。
 * - 数组本身不单独存污点，污点完全由元素承载（供 join 等算子收集）。
 */
export class TArray {
  private readonly elements: TVal[] = [];
  private readonly registry: TaintRegistry;

  constructor(registry: TaintRegistry, initial: Val[] = []) {
    this.registry = registry;
    for (const value of initial) {
      this.push(value);
    }
  }

  push(value: Val): void {
    this.elements.push(this.box(value));
  }

  /** 下标赋值 $arr[$i] = $v：传播右值污点。 */
  set(index: number, value: Val): void {
    this.elements[index] = this.box(value);
  }

  get(index: number): TVal<Literal> {
    const found = this.elements[index];
    if (found) {
      return found;
    }
    return new TVal(undefined, [], this.registry);
  }

  get length(): number {
    return this.elements.length;
  }

  toTVals(): TVal[] {
    return [...this.elements];
  }

  private box(value: Val): TVal {
    if (value instanceof TVal) {
      return value;
    }
    return new TVal(value, [], this.registry);
  }
}

interface HashEntry {
  key: TVal<string>;
  value: TVal;
}

/**
 * 带污点标记的哈希（关联数组）。
 * - 哈希值传播：$h{$k} = $v 后，get 取回的 value 沿用 $v 的污点。
 * - 键也可带污点：通过污点键访问时，取出的值额外挂一条 hash-get 事实，
 *   使键来源同样进入来源链。
 */
export class THash {
  private readonly entries = new Map<string, HashEntry>();
  private readonly registry: TaintRegistry;

  constructor(registry: TaintRegistry) {
    this.registry = registry;
  }

  set(key: Val, value: Val): void {
    const keyTVal = this.boxKey(key);
    const valueTVal = this.box(value);
    this.entries.set(String(keyTVal.raw), { key: keyTVal, value: valueTVal });
  }

  get(key: Val): TVal {
    const keyTVal = this.boxKey(key);
    const entry = this.entries.get(String(keyTVal.raw));

    if (!entry) {
      // 即使键不存在，污点键本身的污点也要传播出去。
      const factId = this.registry.link(
        "hash-get-missing",
        `key=${previewValue(keyTVal.raw)}`,
        [...keyTVal.factIds],
      );
      return new TVal(undefined, factId === undefined ? [] : [factId], this.registry);
    }

    if (!keyTVal.isTainted) {
      return entry.value;
    }

    const factId = this.registry.link(
      "hash-get",
      `key=${previewValue(keyTVal.raw)}`,
      [...keyTVal.factIds, ...entry.value.factIds],
    );
    return new TVal(
      entry.value.raw,
      factId === undefined ? [...entry.value.factIds] : [factId],
      this.registry,
    );
  }

  has(key: Val): boolean {
    return this.entries.has(String(this.boxKey(key).raw));
  }

  private box(value: Val): TVal {
    if (value instanceof TVal) {
      return value;
    }
    return new TVal(value, [], this.registry);
  }

  private boxKey(key: Val): TVal<string> {
    if (key instanceof TVal) {
      return new TVal(String(key.raw), [...key.factIds], this.registry);
    }
    return new TVal(String(key), [], this.registry);
  }
}
