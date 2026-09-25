/**
 * 只读显式属性存储。
 * 策略只能读取求值时显式提供的属性；属性域限定为：整数、布尔、有限字符串集合。
 * 不接外部身份服务：所有属性必须在求值前一次性注入，求值期间不可变。
 */

export type AttributeValue =
  | { kind: 'int'; value: number }
  | { kind: 'bool'; value: boolean }
  | { kind: 'set'; value: readonly string[] };

export const intAttr = (value: number): AttributeValue => {
  if (!Number.isInteger(value)) throw new Error(`整数属性要求整数值，得到: ${value}`);
  return { kind: 'int', value };
};
export const boolAttr = (value: boolean): AttributeValue => ({ kind: 'bool', value });
export const setAttr = (value: readonly string[]): AttributeValue =>
  ({ kind: 'set', value: Object.freeze([...value]) });

export class AttributeStore {
  readonly #attrs: ReadonlyMap<string, AttributeValue>;

  constructor(attrs: Record<string, AttributeValue>) {
    // 构造时快照并冻结：之后无法修改，保证“只读显式属性”。
    this.#attrs = new Map(Object.entries(attrs));
    Object.freeze(this);
  }

  has(name: string): boolean {
    return this.#attrs.has(name);
  }

  get(name: string): AttributeValue {
    const v = this.#attrs.get(name);
    if (v === undefined) {
      // 未知属性是明确失败，而不是静默当作假。
      throw new Error(`属性未定义: ${name}（策略只读显式属性，不存在外部身份服务可查询）`);
    }
    return v;
  }

  getInt(name: string): number {
    const v = this.get(name);
    if (v.kind !== 'int') throw new Error(`属性 ${name} 不是整数类型`);
    return v.value;
  }

  getBool(name: string): boolean {
    const v = this.get(name);
    if (v.kind !== 'bool') throw new Error(`属性 ${name} 不是布尔类型`);
    return v.value;
  }

  getSet(name: string): readonly string[] {
    const v = this.get(name);
    if (v.kind !== 'set') throw new Error(`属性 ${name} 不是有限集合类型`);
    return v.value;
  }

  /** 不提供任何 set/delete 方法；尝试修改构造入参不会影响本存储（已快照）。 */
  names(): string[] {
    return [...this.#attrs.keys()];
  }
}
