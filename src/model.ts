// 数据模型：有限整数域 / 枚举属性、条件、规则、输入。

export interface IntAttr {
  kind: 'int';
  name: string;
  /** 显式有限边界，闭区间 [min, max]，均为整数。 */
  min: number;
  max: number;
}

export interface EnumAttr {
  kind: 'enum';
  name: string;
  /** 有限取值集合；数组顺序即“最小反例”比较时的字典序。 */
  values: string[];
}

export type Attr = IntAttr | EnumAttr;

export interface Schema {
  /** 属性顺序即输入字典序的比较顺序。 */
  attrs: Attr[];
}

/** 整数条件：半开区间 [lo, hi)。lo >= hi 表示空区间（永不匹配）。 */
export interface IntCondition {
  kind: 'int';
  attr: string;
  lo: number;
  hi: number;
}

/** 枚举条件：取值属于 in 集合。空集合表示永不匹配。 */
export interface EnumCondition {
  kind: 'enum';
  attr: string;
  in: string[];
}

export type Condition = IntCondition | EnumCondition;

export interface Rule {
  id: string;
  action: string;
  /** 条件之间为合取（AND）；同一属性出现多个条件时取交集。 */
  conditions: Condition[];
}

export type InputValue = number | string;
export type Input = Record<string, InputValue>;

// ---- 条件构造辅助 ----

/** 半开区间 [lo, hi)。 */
export function intRange(attr: string, lo: number, hi: number): IntCondition {
  return { kind: 'int', attr, lo, hi };
}

/** 闭区间 [lo, hiInclusive]，内部转为半开 [lo, hiInclusive+1)。 */
export function intBetween(attr: string, lo: number, hiInclusive: number): IntCondition {
  return { kind: 'int', attr, lo, hi: hiInclusive + 1 };
}

/** 单点 [v, v+1)。 */
export function intEq(attr: string, v: number): IntCondition {
  return { kind: 'int', attr, lo: v, hi: v + 1 };
}

export function enumIn(attr: string, values: string[]): EnumCondition {
  return { kind: 'enum', attr, in: [...values] };
}

export function enumEq(attr: string, value: string): EnumCondition {
  return { kind: 'enum', attr, in: [value] };
}
