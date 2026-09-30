import { Unit } from './unit.ts';
import { Rational, type RationalLike } from './rational.ts';

export class UnknownUnitError extends Error {
  constructor(symbol: string) {
    super(`未注册的单位: ${symbol}`);
    this.name = 'UnknownUnitError';
  }
}

export class DimensionMismatchError extends Error {
  constructor(from: Unit, to: Unit) {
    super(
      `量纲不一致，拒绝转换：${from.symbol}（${from.dimension.describe()}）` +
        `不能转换为 ${to.symbol}（${to.dimension.describe()}）`,
    );
    this.name = 'DimensionMismatchError';
  }
}

export class ContradictoryConversionError extends Error {
  constructor(from: string, to: string, existing: Rational, incoming: Rational) {
    super(
      `矛盾转换边被拒绝：${from} → ${to} 已存在换算比例 ${existing.toString()}，` +
        `新边声明 ${incoming.toString()}，二者构成矛盾环`,
    );
    this.name = 'ContradictoryConversionError';
  }
}

export class NoConversionPathError extends Error {
  constructor(from: string, to: string) {
    super(`转换图中不存在从 ${from} 到 ${to} 的换算路径`);
    this.name = 'NoConversionPathError';
  }
}

/**
 * 转换图：单位是节点，"1 from = factor to" 的精确有理数比例是边。
 *
 * - 加边前先检查量纲一致性（米不能转秒）；
 * - 若两点间已存在路径，则新边比例必须与路径合成比例完全一致，
 *   否则构成矛盾环，拒绝加边；
 * - 边只表达线性（差量）比例；绝对量的零点偏移是单位自身属性，
 *   由 Quantity 在转换时单独处理。
 */
export class UnitRegistry {
  private readonly units = new Map<string, Unit>();
  private readonly edges = new Map<string, Map<string, Rational>>();
  private readonly baseByDimension = new Map<string, string>();

  has(symbol: string): boolean {
    return this.units.has(symbol);
  }

  get(symbol: string): Unit {
    const unit = this.units.get(symbol);
    if (!unit) throw new UnknownUnitError(symbol);
    return unit;
  }

  /** 注册某量纲的基准单位（scale 必须为 1、offset 必须为 0）。 */
  registerBaseUnit(unit: Unit): this {
    if (!unit.scale.equals(Rational.one) || !unit.offset.isZero()) {
      throw new RangeError(`基准单位 ${unit.symbol} 必须 scale=1 且 offset=0`);
    }
    const key = unit.dimension.key;
    const existing = this.baseByDimension.get(key);
    if (existing !== undefined && existing !== unit.symbol) {
      throw new ContradictoryConversionError(
        existing,
        unit.symbol,
        Rational.one,
        unit.scale,
      );
    }
    this.addNode(unit);
    this.baseByDimension.set(key, unit.symbol);
    return this;
  }

  /** 注册导出单位：自动向该量纲的基准单位连一条 scale 边。 */
  registerUnit(unit: Unit): this {
    const baseSymbol = this.baseByDimension.get(unit.dimension.key);
    if (baseSymbol === undefined) {
      throw new UnknownUnitError(
        `量纲 ${unit.dimension.toString()} 的基准单位（请先注册基准单位）`,
      );
    }
    this.addNode(unit);
    if (unit.symbol !== baseSymbol) {
      this.addConversion(unit.symbol, baseSymbol, unit.scale);
    }
    return this;
  }

  /**
   * 声明一条换算边 "1 from = factor to"（自动补反向边）。
   * 返回 true 表示新增了边；false 表示与已有路径一致、为冗余边。
   * 量纲不符或与已有路径矛盾时抛错，图保持不变。
   */
  addConversion(fromSymbol: string, toSymbol: string, factor: RationalLike): boolean {
    const from = this.get(fromSymbol);
    const to = this.get(toSymbol);
    if (!from.dimension.equals(to.dimension)) {
      throw new DimensionMismatchError(from, to);
    }
    const ratio = Rational.of(factor);
    if (ratio.isZero()) throw new RangeError('换算比例不能为 0');

    const existing = this.findFactor(fromSymbol, toSymbol);
    if (existing !== undefined) {
      if (!existing.equals(ratio)) {
        throw new ContradictoryConversionError(fromSymbol, toSymbol, existing, ratio);
      }
      return false;
    }
    this.addEdge(fromSymbol, toSymbol, ratio);
    this.addEdge(toSymbol, fromSymbol, ratio.reciprocal());
    return true;
  }

  /** 1 from = factor(from, to) to；无路径时抛 NoConversionPathError。 */
  factor(fromSymbol: string, toSymbol: string): Rational {
    const from = this.get(fromSymbol);
    const to = this.get(toSymbol);
    if (!from.dimension.equals(to.dimension)) {
      throw new DimensionMismatchError(from, to);
    }
    const found = this.findFactor(fromSymbol, toSymbol);
    if (found === undefined) throw new NoConversionPathError(fromSymbol, toSymbol);
    return found;
  }

  private addNode(unit: Unit): void {
    this.units.set(unit.symbol, unit);
    if (!this.edges.has(unit.symbol)) this.edges.set(unit.symbol, new Map());
  }

  private addEdge(from: string, to: string, factor: Rational): void {
    this.edges.get(from)!.set(to, factor);
  }

  /** BFS 合成路径比例；起点即终点时比例为 1；无路径返回 undefined。 */
  private findFactor(fromSymbol: string, toSymbol: string): Rational | undefined {
    if (!this.units.has(fromSymbol) || !this.units.has(toSymbol)) return undefined;
    if (fromSymbol === toSymbol) return Rational.one;
    const visited = new Map<string, Rational>([[fromSymbol, Rational.one]]);
    const queue: string[] = [fromSymbol];
    while (queue.length > 0) {
      const current = queue.shift()!;
      const currentFactor = visited.get(current)!;
      for (const [next, edge] of this.edges.get(current) ?? []) {
        if (visited.has(next)) continue;
        const composed = currentFactor.mul(edge);
        if (next === toSymbol) return composed;
        visited.set(next, composed);
        queue.push(next);
      }
    }
    return undefined;
  }
}
