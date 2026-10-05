/**
 * 区域（box）代数：把规则条件表示为各属性维度的笛卡尔积，
 * 支持交、差、最小见证点等精确符号运算（非抽样）。
 */

import type { Domain, Input, Rule } from './model.ts';

export type IntDim = { kind: 'int'; lo: number; hi: number }; // 闭区间；lo > hi 为空
export type EnumDim = { kind: 'enum'; values: readonly string[] }; // 空数组为空
export type Dim = IntDim | EnumDim;

/** 与 domain.attrs 对齐的各维约束之积 */
export type Box = { dims: Dim[] };

export function fullBox(domain: Domain): Box {
  return {
    dims: domain.attrs.map((a): Dim =>
      a.kind === 'int' ? { kind: 'int', lo: a.min, hi: a.max } : { kind: 'enum', values: a.values },
    ),
  };
}

export function isEmpty(box: Box): boolean {
  return box.dims.some((d) => (d.kind === 'int' ? d.lo > d.hi : d.values.length === 0));
}

function intersectDim(a: Dim, b: Dim): Dim {
  if (a.kind === 'int' && b.kind === 'int') {
    return { kind: 'int', lo: Math.max(a.lo, b.lo), hi: Math.min(a.hi, b.hi) };
  }
  if (a.kind === 'enum' && b.kind === 'enum') {
    const setB = new Set(b.values);
    return { kind: 'enum', values: a.values.filter((v) => setB.has(v)) };
  }
  throw new Error('dimension kind mismatch');
}

export function intersect(a: Box, b: Box): Box | null {
  const dims = a.dims.map((d, i) => intersectDim(d, b.dims[i]));
  const box: Box = { dims };
  return isEmpty(box) ? null : box;
}

/** 单维差：a \ b。整数维最多裂成两段，枚举维为集合差。 */
function subtractDim(a: Dim, b: Dim): Dim[] {
  if (a.kind === 'int' && b.kind === 'int') {
    const parts: Dim[] = [];
    const loHi = Math.min(a.hi, b.lo - 1);
    if (a.lo <= loHi) parts.push({ kind: 'int', lo: a.lo, hi: loHi });
    const hiLo = Math.max(a.lo, b.hi + 1);
    if (hiLo <= a.hi) parts.push({ kind: 'int', lo: hiLo, hi: a.hi });
    return parts;
  }
  if (a.kind === 'enum' && b.kind === 'enum') {
    const setB = new Set(b.values);
    const rest = a.values.filter((v) => !setB.has(v));
    return rest.length > 0 ? [{ kind: 'enum', values: rest }] : [];
  }
  throw new Error('dimension kind mismatch');
}

/**
 * 盒差 a \ b，返回互不重叠的盒列表。
 * 逐维扫描：第 i 维取 a_i \ b_i，左侧各维取 a∩b，右侧各维取 a。
 */
export function subtract(a: Box, b: Box): Box[] {
  if (isEmpty(a)) return [];
  if (isEmpty(b) || intersect(a, b) === null) return [a];
  const out: Box[] = [];
  const prefix: Dim[] = [];
  for (let i = 0; i < a.dims.length; i++) {
    for (const part of subtractDim(a.dims[i], b.dims[i])) {
      out.push({ dims: [...prefix, part, ...a.dims.slice(i + 1)] });
    }
    prefix.push(intersectDim(a.dims[i], b.dims[i]));
  }
  return out.filter((box) => !isEmpty(box));
}

export function subtractMany(a: Box, bs: readonly Box[]): Box[] {
  let rest = [a];
  for (const b of bs) rest = rest.flatMap((box) => subtract(box, b));
  return rest;
}

/** 由规则条件构造盒；同一属性上的多个条件取交。未知属性/枚举值直接报错。 */
export function boxFromRule(domain: Domain, rule: Rule): Box {
  const box = fullBox(domain);
  for (const cond of rule.conds) {
    const i = domain.attrs.findIndex((a) => a.name === cond.attr);
    if (i < 0) throw new Error(`rule ${rule.id}: unknown attribute ${cond.attr}`);
    const attr = domain.attrs[i];
    if (cond.kind === 'int') {
      if (attr.kind !== 'int') throw new Error(`rule ${rule.id}: ${cond.attr} is not an int attribute`);
      const lo = Math.max(cond.loOpen ? cond.lo + 1 : cond.lo, attr.min);
      const hi = Math.min(cond.hiOpen ? cond.hi - 1 : cond.hi, attr.max);
      box.dims[i] = intersectDim(box.dims[i], { kind: 'int', lo, hi });
    } else {
      if (attr.kind !== 'enum') throw new Error(`rule ${rule.id}: ${cond.attr} is not an enum attribute`);
      for (const v of cond.values) {
        if (!attr.values.includes(v)) throw new Error(`rule ${rule.id}: unknown value ${v} for ${cond.attr}`);
      }
      box.dims[i] = intersectDim(box.dims[i], { kind: 'enum', values: cond.values });
    }
  }
  return box;
}

/** 盒的测度（精确计数，整数维为区间长度，枚举维为取值数） */
export function boxSize(box: Box): number {
  if (isEmpty(box)) return 0;
  return box.dims.reduce((acc, d) => acc * (d.kind === 'int' ? d.hi - d.lo + 1 : d.values.length), 1);
}

export function contains(box: Box, domain: Domain, input: Input): boolean {
  return box.dims.every((d, i) => {
    const v = input[domain.attrs[i].name];
    return d.kind === 'int'
      ? typeof v === 'number' && Number.isInteger(v) && v >= d.lo && v <= d.hi
      : typeof v === 'string' && d.values.includes(v);
  });
}

/** 盒内字典序最小见证点：整数取下界，枚举取域中首个允许值 */
export function minPoint(box: Box, domain: Domain): Input {
  const out: Input = {};
  box.dims.forEach((d, i) => {
    out[domain.attrs[i].name] = d.kind === 'int' ? d.lo : d.values[0];
  });
  return out;
}

export function compareInputs(domain: Domain, a: Input, b: Input): number {
  for (const attr of domain.attrs) {
    const va = a[attr.name];
    const vb = b[attr.name];
    if (attr.kind === 'int') {
      const d = (va as number) - (vb as number);
      if (d !== 0) return d < 0 ? -1 : 1;
    } else {
      const d = attr.values.indexOf(va as string) - attr.values.indexOf(vb as string);
      if (d !== 0) return d < 0 ? -1 : 1;
    }
  }
  return 0;
}

/** 一组盒中的全局最小见证点（最小反例） */
export function minPointOfBoxes(boxes: readonly Box[], domain: Domain): Input | undefined {
  let best: Input | undefined;
  for (const box of boxes) {
    if (isEmpty(box)) continue;
    const p = minPoint(box, domain);
    if (best === undefined || compareInputs(domain, p, best) < 0) best = p;
  }
  return best;
}

export function formatBox(box: Box, domain: Domain): string {
  return box.dims
    .map((d, i) => {
      const name = domain.attrs[i].name;
      return d.kind === 'int' ? `${name}∈[${d.lo},${d.hi}]` : `${name}∈{${d.values.join(',')}}`;
    })
    .join(' ∧ ');
}
