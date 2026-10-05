/**
 * 规则覆盖分析器 —— 数据模型。
 *
 * 规则按顺序匹配，首个命中的规则决定动作（first-match 语义）。
 * 整数域边界显式有限：每个整数属性必须给出 [min, max] 闭区间。
 */

export type IntAttr = {
  kind: 'int';
  name: string;
  /** 有限下界（含） */
  min: number;
  /** 有限上界（含） */
  max: number;
};

export type EnumAttr = {
  kind: 'enum';
  name: string;
  /** 有限取值集合，数组顺序即“最小反例”比较时的顺序 */
  values: readonly string[];
};

export type Attr = IntAttr | EnumAttr;

export type Domain = { attrs: readonly Attr[] };

/** 整数区间条件，端点可开可闭；内部统一归一化为闭区间 */
export type IntCond = {
  kind: 'int';
  attr: string;
  lo: number;
  hi: number;
  /** true 表示左端点开区间 (lo, ...] */
  loOpen?: boolean;
  /** true 表示右端点开区间 [..., hi) */
  hiOpen?: boolean;
};

export type EnumCond = {
  kind: 'enum';
  attr: string;
  values: readonly string[];
};

export type Cond = IntCond | EnumCond;

/** 一条规则：所有条件取合取；未提及的属性视为不受约束 */
export type Rule = {
  id: string;
  action: string;
  conds: readonly Cond[];
};

export type Input = Record<string, number | string>;

export function attrOf(domain: Domain, name: string): Attr {
  const attr = domain.attrs.find((a) => a.name === name);
  if (!attr) throw new Error(`unknown attribute: ${name}`);
  return attr;
}
