// 盒（Box）：各属性约束的笛卡尔积，是覆盖分析的符号表示。
// 整数维为半开区间 [lo, hi)，枚举维为取值集合（按 schema 顺序排列）。
// 所有运算均为精确集合运算，不做任何抽样。

import type { Attr, Input, InputValue, Rule, Schema } from './model.ts';

export interface IntInterval {
  lo: number;
  hi: number; // 排他
}

export interface Box {
  /** 每个整数属性都有对应区间；lo >= hi 表示空。 */
  ints: Record<string, IntInterval>;
  /** 每个枚举属性都有对应取值集合；空数组表示空。 */
  enums: Record<string, string[]>;
}

/** Schema 的预计算索引：属性查表与枚举值名次。 */
export interface SchemaIndex {
  schema: Schema;
  attrByName: Map<string, Attr>;
  enumRank: Map<string, Map<string, number>>;
}

export function indexSchema(schema: Schema): SchemaIndex {
  const attrByName = new Map<string, Attr>();
  const enumRank = new Map<string, Map<string, number>>();
  for (const attr of schema.attrs) {
    if (attrByName.has(attr.name)) {
      throw new Error(`duplicate attribute: ${attr.name}`);
    }
    attrByName.set(attr.name, attr);
    if (attr.kind === 'enum') {
      if (attr.values.length === 0) {
        throw new Error(`enum attribute ${attr.name} has empty domain`);
      }
      const rank = new Map<string, number>();
      attr.values.forEach((v, i) => {
        if (rank.has(v)) throw new Error(`enum attribute ${attr.name} has duplicate value: ${v}`);
        rank.set(v, i);
      });
      enumRank.set(attr.name, rank);
    } else {
      if (!Number.isInteger(attr.min) || !Number.isInteger(attr.max) || attr.min > attr.max) {
        throw new Error(`int attribute ${attr.name} has invalid domain [${attr.min}, ${attr.max}]`);
      }
    }
  }
  return { schema, attrByName, enumRank };
}

/** 全域盒：每个属性取整个有限域。整数域 [min, max] 表示为 [min, max+1)。 */
export function fullBox(idx: SchemaIndex): Box {
  const ints: Record<string, IntInterval> = {};
  const enums: Record<string, string[]> = {};
  for (const attr of idx.schema.attrs) {
    if (attr.kind === 'int') {
      ints[attr.name] = { lo: attr.min, hi: attr.max + 1 };
    } else {
      enums[attr.name] = [...attr.values];
    }
  }
  return { ints, enums };
}

export function cloneBox(box: Box): Box {
  const ints: Record<string, IntInterval> = {};
  for (const k of Object.keys(box.ints)) ints[k] = { ...box.ints[k] };
  const enums: Record<string, string[]> = {};
  for (const k of Object.keys(box.enums)) enums[k] = [...box.enums[k]];
  return { ints, enums };
}

export function isEmptyBox(box: Box): boolean {
  for (const k of Object.keys(box.ints)) {
    if (box.ints[k].lo >= box.ints[k].hi) return true;
  }
  for (const k of Object.keys(box.enums)) {
    if (box.enums[k].length === 0) return true;
  }
  return false;
}

/** 由规则条件构建盒：从全域出发逐条件相交；同一属性多条件自然取交集。 */
export function ruleBox(idx: SchemaIndex, rule: Rule): Box {
  const box = fullBox(idx);
  for (const cond of rule.conditions) {
    const attr = idx.attrByName.get(cond.attr);
    if (!attr) throw new Error(`rule ${rule.id}: unknown attribute ${cond.attr}`);
    if (cond.kind === 'int') {
      if (attr.kind !== 'int') throw new Error(`rule ${rule.id}: attribute ${cond.attr} is not an int`);
      if (!Number.isInteger(cond.lo) || !Number.isInteger(cond.hi)) {
        throw new Error(`rule ${rule.id}: non-integer bound on ${cond.attr}`);
      }
      const cur = box.ints[cond.attr];
      box.ints[cond.attr] = { lo: Math.max(cur.lo, cond.lo), hi: Math.min(cur.hi, cond.hi) };
    } else {
      if (attr.kind !== 'enum') throw new Error(`rule ${rule.id}: attribute ${cond.attr} is not an enum`);
      const rank = idx.enumRank.get(cond.attr)!;
      for (const v of cond.in) {
        if (!rank.has(v)) throw new Error(`rule ${rule.id}: value ${v} not in domain of ${cond.attr}`);
      }
      const allow = new Set(cond.in);
      box.enums[cond.attr] = box.enums[cond.attr].filter((v) => allow.has(v));
    }
  }
  return box;
}

/** 逐维相交；结果可能为空盒（用 isEmptyBox 判定）。 */
export function intersectBoxes(a: Box, b: Box): Box {
  const ints: Record<string, IntInterval> = {};
  for (const k of Object.keys(a.ints)) {
    ints[k] = {
      lo: Math.max(a.ints[k].lo, b.ints[k].lo),
      hi: Math.min(a.ints[k].hi, b.ints[k].hi),
    };
  }
  const enums: Record<string, string[]> = {};
  for (const k of Object.keys(a.enums)) {
    const allow = new Set(b.enums[k]);
    enums[k] = a.enums[k].filter((v) => allow.has(v));
  }
  return { ints, enums };
}

export function boxesOverlap(a: Box, b: Box): boolean {
  return !isEmptyBox(intersectBoxes(a, b));
}

/**
 * 盒差：b \ a，返回互不重叠的盒列表。
 * 若不相交返回 [b]；否则沿各维把 b 劈成“低于 a / 高于 a（或集合差）”的碎片，
 * 与 a 的交集部分被丢弃。结果为精确集合差。
 */
export function subtractBox(idx: SchemaIndex, b: Box, a: Box): Box[] {
  const inter = intersectBoxes(b, a);
  if (isEmptyBox(inter)) return [cloneBox(b)];

  const result: Box[] = [];
  let rest = cloneBox(b);

  for (const attr of idx.schema.attrs) {
    if (attr.kind !== 'int') continue;
    const r = rest.ints[attr.name];
    const i = inter.ints[attr.name];
    if (r.lo < i.lo) {
      const low = cloneBox(rest);
      low.ints[attr.name] = { lo: r.lo, hi: i.lo };
      result.push(low);
      rest.ints[attr.name] = { lo: i.lo, hi: r.hi };
    }
    if (i.hi < rest.ints[attr.name].hi) {
      const cur = rest.ints[attr.name];
      const high = cloneBox(rest);
      high.ints[attr.name] = { lo: i.hi, hi: cur.hi };
      result.push(high);
      rest.ints[attr.name] = { lo: cur.lo, hi: i.hi };
    }
  }

  for (const attr of idx.schema.attrs) {
    if (attr.kind !== 'enum') continue;
    const keep = new Set(inter.enums[attr.name]);
    const diff = rest.enums[attr.name].filter((v) => !keep.has(v));
    if (diff.length > 0) {
      const side = cloneBox(rest);
      side.enums[attr.name] = diff;
      result.push(side);
      rest.enums[attr.name] = rest.enums[attr.name].filter((v) => keep.has(v));
    }
  }
  // 此时 rest == inter（b 与 a 的交集），丢弃。
  return result;
}

/** b 依次减去若干盒：B \ (A1 ∪ A2 ∪ ...) = ((B \ A1) \ A2) \ ...，精确且保持互不重叠。 */
export function subtractMany(idx: SchemaIndex, b: Box, others: Box[]): Box[] {
  let boxes: Box[] = [cloneBox(b)];
  for (const a of others) {
    if (isEmptyBox(a)) continue;
    const next: Box[] = [];
    for (const cur of boxes) {
      for (const piece of subtractBox(idx, cur, a)) {
        if (!isEmptyBox(piece)) next.push(piece);
      }
    }
    boxes = next;
    if (boxes.length === 0) break;
  }
  return boxes;
}

/** 盒的体积（覆盖的输入个数）。空盒为 0。 */
export function boxVolume(idx: SchemaIndex, box: Box): number {
  let vol = 1;
  for (const attr of idx.schema.attrs) {
    if (attr.kind === 'int') {
      const r = box.ints[attr.name];
      const n = r.hi - r.lo;
      if (n <= 0) return 0;
      vol *= n;
    } else {
      const n = box.enums[attr.name].length;
      if (n === 0) return 0;
      vol *= n;
    }
  }
  return vol;
}

/** 盒内字典序最小点（要求非空盒）。 */
export function minPoint(idx: SchemaIndex, box: Box): Input {
  const input: Input = {};
  for (const attr of idx.schema.attrs) {
    if (attr.kind === 'int') {
      input[attr.name] = box.ints[attr.name].lo;
    } else {
      input[attr.name] = box.enums[attr.name][0];
    }
  }
  return input;
}

/** 一组盒的总体积（调用方保证互不重叠，subtractMany 的结果满足）。 */
export function sumVolumes(idx: SchemaIndex, boxes: Box[]): number {
  let sum = 0;
  for (const b of boxes) sum += boxVolume(idx, b);
  return sum;
}

/** 一组互不重叠非空盒的最小点；空列表返回 null。 */
export function minPointOfBoxes(idx: SchemaIndex, boxes: Box[]): Input | null {
  let best: Input | null = null;
  for (const b of boxes) {
    if (isEmptyBox(b)) continue;
    const p = minPoint(idx, b);
    if (best === null || compareInputs(idx, p, best) < 0) best = p;
  }
  return best;
}

/** 输入字典序：按 schema 属性顺序；整数按数值，枚举按 schema 中声明顺序。 */
export function compareInputs(idx: SchemaIndex, x: Input, y: Input): number {
  for (const attr of idx.schema.attrs) {
    const xv: InputValue = x[attr.name];
    const yv: InputValue = y[attr.name];
    if (attr.kind === 'int') {
      const d = (xv as number) - (yv as number);
      if (d !== 0) return d < 0 ? -1 : 1;
    } else {
      const rank = idx.enumRank.get(attr.name)!;
      const d = rank.get(xv as string)! - rank.get(yv as string)!;
      if (d !== 0) return d < 0 ? -1 : 1;
    }
  }
  return 0;
}

export function containsPoint(idx: SchemaIndex, box: Box, input: Input): boolean {
  for (const attr of idx.schema.attrs) {
    const v = input[attr.name];
    if (attr.kind === 'int') {
      const r = box.ints[attr.name];
      if (typeof v !== 'number' || v < r.lo || v >= r.hi) return false;
    } else {
      if (!box.enums[attr.name].includes(v as string)) return false;
    }
  }
  return true;
}

export function formatInput(idx: SchemaIndex, input: Input): string {
  const parts: string[] = [];
  for (const attr of idx.schema.attrs) {
    parts.push(`${attr.name}=${JSON.stringify(input[attr.name])}`);
  }
  return `{${parts.join(', ')}}`;
}

/** 盒的可读描述，用于报告展示。 */
export function formatBox(idx: SchemaIndex, box: Box): string {
  const parts: string[] = [];
  for (const attr of idx.schema.attrs) {
    if (attr.kind === 'int') {
      const r = box.ints[attr.name];
      parts.push(`${attr.name}∈[${r.lo}, ${r.hi})`);
    } else {
      parts.push(`${attr.name}∈{${box.enums[attr.name].join(', ')}}`);
    }
  }
  return parts.join(' ∧ ');
}
