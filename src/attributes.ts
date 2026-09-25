import { assertAttrValue, type AttrValue } from './conditions.ts';

export type Scope = 'subject' | 'context';

/** 属性的规范键：scope:ownerId:name，主体与上下文各自隔离。 */
export function attrKey(scope: Scope, ownerId: string, name: string): string {
  return `${scope}:${ownerId}:${name}`;
}

export function parseAttrRef(ref: string): { scope: Scope; name: string } {
  const dot = ref.indexOf('.');
  const scope = ref.slice(0, dot);
  if (scope !== 'subject' && scope !== 'context' || dot < 0) {
    throw new TypeError(`非法属性引用 "${ref}"，必须是 subject.<name> 或 context.<name>`);
  }
  return { scope, name: ref.slice(dot + 1) };
}

/** 显式属性仓库：唯一的属性来源，不接外部身份服务。 */
export class AttributeStore {
  readonly #values = new Map<string, AttrValue>();

  getByKey(key: string): AttrValue | undefined {
    return this.#values.get(key);
  }

  has(scope: Scope, ownerId: string, name: string): boolean {
    return this.#values.has(attrKey(scope, ownerId, name));
  }

  /** @returns 值是否真的发生了变化（未变化时不应触发失效） */
  set(scope: Scope, ownerId: string, name: string, value: AttrValue): boolean {
    assertAttrValue(value, `set ${scope}.${name}`);
    const key = attrKey(scope, ownerId, name);
    if (this.#values.has(key) && this.#values.get(key) === value) return false;
    this.#values.set(key, value);
    return true;
  }

  /** @returns 属性是否原本存在（不存在的删除无需失效） */
  delete(scope: Scope, ownerId: string, name: string): boolean {
    return this.#values.delete(attrKey(scope, ownerId, name));
  }
}
