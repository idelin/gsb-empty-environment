/**
 * 条件表达式：仅允许有限集合成员、整数比较与布尔运算。
 * 所有条件节点做哈希共享（hash-consing），相同结构的条件在全引擎内只存一份。
 */

export type Attrs = Readonly<Record<string, number>>;

export type Cond =
  | { kind: 'const'; value: boolean }
  | { kind: 'cmp'; attr: string; op: 'eq' | 'lt' | 'le' | 'gt' | 'ge'; value: number }
  | { kind: 'between'; attr: string; lo: number; hi: number } // lo <= x < hi（半开）
  | { kind: 'in'; attr: string; values: readonly number[] } // 有限整数集合
  | { kind: 'and'; children: readonly Cond[] }
  | { kind: 'or'; children: readonly Cond[] }
  | { kind: 'not'; child: Cond };

export const ALWAYS: Cond = { kind: 'const', value: true };

function assertInt(n: number, what: string): void {
  if (!Number.isInteger(n)) throw new Error(`${what} 必须是整数，得到 ${n}`);
}

/** 校验条件合法性：有限集合、整数条件、布尔运算，拒绝其它一切。 */
export function validateCond(cond: Cond): void {
  switch (cond.kind) {
    case 'const':
      return;
    case 'cmp':
      if (!cond.attr) throw new Error('cmp 缺少属性名');
      assertInt(cond.value, 'cmp.value');
      return;
    case 'between':
      if (!cond.attr) throw new Error('between 缺少属性名');
      assertInt(cond.lo, 'between.lo');
      assertInt(cond.hi, 'between.hi');
      if (!(cond.lo < cond.hi)) throw new Error(`between 需要 lo < hi，得到 [${cond.lo}, ${cond.hi})`);
      return;
    case 'in': {
      if (!cond.attr) throw new Error('in 缺少属性名');
      if (!Array.isArray(cond.values) || cond.values.length === 0) throw new Error('in 需要非空有限集合');
      let prev = -Infinity;
      for (const v of cond.values) {
        assertInt(v, 'in.values 元素');
        if (v <= prev) throw new Error('in.values 必须严格升序去重');
        prev = v;
      }
      return;
    }
    case 'and':
    case 'or':
      if (!Array.isArray(cond.children) || cond.children.length === 0) throw new Error(`${cond.kind} 需要非空子条件`);
      for (const c of cond.children) validateCond(c);
      return;
    case 'not':
      validateCond(cond.child);
      return;
    default:
      throw new Error(`未知条件类型: ${JSON.stringify(cond)}`);
  }
}

/** 规范化序列化：and/or 子节点按键排序（交换律归一），保证结构等价 => 键相同。 */
export function condKey(cond: Cond): string {
  switch (cond.kind) {
    case 'const':
      return `T:${cond.value}`;
    case 'cmp':
      return `C:${cond.attr}${cond.op}${cond.value}`;
    case 'between':
      return `B:${cond.attr}[${cond.lo},${cond.hi})`;
    case 'in':
      return `I:${cond.attr}{${cond.values.join(',')}}`;
    case 'not':
      return `N(${condKey(cond.child)})`;
    case 'and':
    case 'or': {
      const keys = cond.children.map(condKey).sort();
      return `${cond.kind === 'and' ? 'A' : 'O'}(${keys.join('|')})`;
    }
  }
}

/** 条件节点共享池：相同结构只保留一个节点 id。 */
export class CondIntern {
  private byKey = new Map<string, number>();
  private nodes: Cond[] = [];

  idOf(cond: Cond): number {
    validateCond(cond);
    const key = condKey(cond);
    const hit = this.byKey.get(key);
    if (hit !== undefined) return hit;
    const id = this.nodes.length;
    this.nodes.push(cond);
    this.byKey.set(key, id);
    return id;
  }

  node(id: number): Cond {
    return this.nodes[id];
  }

  get size(): number {
    return this.nodes.length;
  }
}

/**
 * 求值：只读取 attrs 中显式给出的属性；属性缺失时该原子条件为 false（不猜、不外查）。
 */
export function evalCond(cond: Cond, attrs: Attrs): boolean {
  switch (cond.kind) {
    case 'const':
      return cond.value;
    case 'cmp': {
      const x = attrs[cond.attr];
      if (x === undefined) return false;
      switch (cond.op) {
        case 'eq': return x === cond.value;
        case 'lt': return x < cond.value;
        case 'le': return x <= cond.value;
        case 'gt': return x > cond.value;
        case 'ge': return x >= cond.value;
      }
      return false;
    }
    case 'between': {
      const x = attrs[cond.attr];
      if (x === undefined) return false;
      return cond.lo <= x && x < cond.hi;
    }
    case 'in': {
      const x = attrs[cond.attr];
      if (x === undefined) return false;
      return cond.values.includes(x);
    }
    case 'and':
      return cond.children.every((c) => evalCond(c, attrs));
    case 'or':
      return cond.children.some((c) => evalCond(c, attrs));
    case 'not':
      return !evalCond(cond.child, attrs);
  }
}
