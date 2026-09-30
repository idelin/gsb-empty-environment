import { Unit } from './unit.ts';
import { UnitRegistry } from './registry.ts';
import { Rational, type RationalLike, type RoundingMode } from './rational.ts';

export type QuantityKind = 'absolute' | 'delta';

/**
 * 带单位的量，区分绝对量（absolute）与差量（delta）：
 *
 * - 绝对量转换：经基准单位换算，scale 与 offset 都参与（0 °C → 273.15 K）；
 * - 差量转换：只乘比例，offset 不参与（温差 10 °C → 10 K，不加 273.15）；
 * - 代数规则：绝对量±差量=绝对量，绝对量−绝对量=差量，
 *   差量±差量=差量；绝对量+绝对量、差量−绝对量 无意义，直接抛错。
 */
export class Quantity {
  readonly kind: QuantityKind;
  readonly value: Rational;
  readonly unit: Unit;
  private readonly registry: UnitRegistry;

  private constructor(kind: QuantityKind, value: Rational, unit: Unit, registry: UnitRegistry) {
    this.kind = kind;
    this.value = value;
    this.unit = unit;
    this.registry = registry;
  }

  static absolute(value: RationalLike, unit: Unit, registry: UnitRegistry): Quantity {
    return new Quantity('absolute', Rational.of(value), unit, registry);
  }

  static delta(value: RationalLike, unit: Unit, registry: UnitRegistry): Quantity {
    return new Quantity('delta', Rational.of(value), unit, registry);
  }

  get isAbsolute(): boolean {
    return this.kind === 'absolute';
  }

  convertTo(target: Unit): Quantity {
    // factor 调用同时完成量纲校验（米不能转秒）与路径存在性校验。
    const factor = this.registry.factor(this.unit.symbol, target.symbol);
    if (this.kind === 'delta') {
      return new Quantity('delta', this.value.mul(factor), target, this.registry);
    }
    // 绝对量：先换算到基准（含 offset），再折到目标单位。
    const inBase = this.value.mul(this.unit.scale).add(this.unit.offset);
    const inTarget = inBase.sub(target.offset).div(target.scale);
    return new Quantity('absolute', inTarget, target, this.registry);
  }

  add(other: Quantity): Quantity {
    if (this.kind === 'absolute' && other.kind === 'absolute') {
      throw new TypeError('绝对量与绝对量不能相加（如 0 °C + 0 °C 无物理意义）');
    }
    const otherValue = other.convertTo(this.unit).value;
    const kind: QuantityKind =
      this.kind === 'delta' && other.kind === 'delta' ? 'delta' : 'absolute';
    return new Quantity(kind, this.value.add(otherValue), this.unit, this.registry);
  }

  sub(other: Quantity): Quantity {
    if (this.kind === 'delta' && other.kind === 'absolute') {
      throw new TypeError('差量不能减绝对量');
    }
    const otherValue = other.convertTo(this.unit).value;
    const kind: QuantityKind =
      this.kind === 'absolute' && other.kind === 'delta' ? 'absolute' : 'delta';
    return new Quantity(kind, this.value.sub(otherValue), this.unit, this.registry);
  }

  /** 精确分数形式，如 "1/100 m (Δ)"。 */
  toString(): string {
    const suffix = this.kind === 'delta' ? ' (Δ)' : '';
    return `${this.value.toString()} ${this.unit.symbol}${suffix}`;
  }

  /** 按显式舍入规则格式化，如 format(2, 'half-even')。 */
  format(places: number, mode: RoundingMode = 'half-even'): string {
    const suffix = this.kind === 'delta' ? ' (Δ)' : '';
    return `${this.value.toDecimalString(places, mode)} ${this.unit.symbol}${suffix}`;
  }
}
