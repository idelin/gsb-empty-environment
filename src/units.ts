/**
 * 单位系统：每个单位 = 维度指数表 + 相对基准单位的精确换算因子（Rat）。
 * 维度不同 = 硬性单位错误，任何情况下不得用容差掩盖。
 */

import { Rat } from './rational.ts';

export class UnitMismatchError extends Error {
  constructor(message: string) {
    super(message);
    this.name = 'UnitMismatchError';
  }
}

export class Unit {
  readonly dims: Readonly<Record<string, number>>;
  readonly factor: Rat;
  readonly name: string;

  private constructor(name: string, dims: Record<string, number>, factor: Rat) {
    this.name = name;
    this.dims = Object.freeze({ ...dims });
    this.factor = factor;
  }

  static define(name: string, dims: Record<string, number>, factor: Rat = Rat.one()): Unit {
    return new Unit(name, dims, factor);
  }

  /** 维度键（不含换算因子），如 "L^1,T^-1"。 */
  dimsKey(): string {
    return Object.keys(this.dims)
      .filter((k) => this.dims[k] !== 0)
      .sort()
      .map((k) => `${k}^${this.dims[k]}`)
      .join(',');
  }

  /** 完整键：维度 + 精确因子。 */
  key(): string {
    return `${this.dimsKey()}@${this.factor.toString()}`;
  }

  sameDim(o: Unit): boolean {
    return this.dimsKey() === o.dimsKey();
  }

  /** 维度与换算因子完全一致。 */
  same(o: Unit): boolean {
    return this.sameDim(o) && this.factor.eq(o.factor);
  }

  toString(): string {
    return this.name;
  }
}

/** 常用单位注册表。 */
export const U = {
  one: Unit.define('1', {}),
  m: Unit.define('m', { L: 1 }),
  cm: Unit.define('cm', { L: 1 }, Rat.of(1n, 100n)),
  mm: Unit.define('mm', { L: 1 }, Rat.of(1n, 1000n)),
  s: Unit.define('s', { T: 1 }),
  min: Unit.define('min', { T: 1 }, Rat.of(60n)),
  usd: Unit.define('USD', { CUR: 1 }),
  cent: Unit.define('cent', { CUR: 1 }, Rat.of(1n, 100n)),
} as const;

/** 精确单位换算：v[from] → v[to]。维度不同直接抛错（不可容差掩盖）。 */
export function convertValue(v: Rat, from: Unit, to: Unit): Rat {
  if (!from.sameDim(to)) {
    throw new UnitMismatchError(`无法换算 ${from.name} -> ${to.name}：维度不同（${from.dimsKey()} vs ${to.dimsKey()}）`);
  }
  return v.mul(from.factor).div(to.factor);
}

const isIdentity = (u: Unit): boolean => u.dimsKey() === '' && u.factor.eq(Rat.one());

export function mulUnit(a: Unit, b: Unit): Unit {
  if (isIdentity(a)) return b;
  if (isIdentity(b)) return a;
  const dims: Record<string, number> = { ...a.dims };
  for (const [k, e] of Object.entries(b.dims)) dims[k] = (dims[k] ?? 0) + e;
  return Unit.define(`(${a.name}*${b.name})`, dims, a.factor.mul(b.factor));
}

export function divUnit(a: Unit, b: Unit): Unit {
  if (isIdentity(b)) return a;
  const dims: Record<string, number> = { ...a.dims };
  for (const [k, e] of Object.entries(b.dims)) dims[k] = (dims[k] ?? 0) - e;
  return Unit.define(`(${a.name}/${b.name})`, dims, a.factor.div(b.factor));
}
