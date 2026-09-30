import { DimensionVector } from './dimension.ts';
import { Rational, type RationalLike } from './rational.ts';

/**
 * 单位 = 量纲向量 + 比例（scale）+ 可选偏移（offset）。
 *
 * 约定（相对该量纲的基准单位）：
 *   绝对量：基准值 = 数值 × scale + offset
 *   差量：  基准差 = 数值 × scale        （offset 不参与）
 *
 * 例如 °C：scale = 1，offset = 273.15（基准为 K）。
 */
export interface UnitDef {
  symbol: string;
  name: string;
  dimension: DimensionVector;
  /** 1 个本单位 = scale 个基准单位（差量/线性部分）。 */
  scale: RationalLike;
  /** 绝对量的零点偏移（以基准单位计），缺省为 0。 */
  offset?: RationalLike;
}

export class Unit {
  readonly symbol: string;
  readonly name: string;
  readonly dimension: DimensionVector;
  readonly scale: Rational;
  readonly offset: Rational;

  constructor(def: UnitDef) {
    if (!def.symbol) throw new TypeError('单位必须有自己的符号');
    this.symbol = def.symbol;
    this.name = def.name;
    this.dimension = def.dimension;
    this.scale = Rational.of(def.scale);
    if (this.scale.isZero()) throw new RangeError(`单位 ${def.symbol} 的 scale 不能为 0`);
    this.offset = def.offset === undefined ? Rational.zero : Rational.of(def.offset);
  }

  /** 是否带非零偏移（如 °C、°F）。 */
  get isAffine(): boolean {
    return !this.offset.isZero();
  }

  toString(): string {
    return `${this.symbol}（${this.name}，量纲 ${this.dimension.toString()}）`;
  }
}
