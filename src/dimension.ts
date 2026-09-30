/**
 * 量纲向量：以 7 个 SI 基本量纲的指数表示。
 * 指数本身是小整数（不会是大数），用 number 表示不涉及精度问题；
 * 所有"数值"才走 BigInt 有理数通道。
 */

export const BASE_DIMENSIONS = ['L', 'M', 'T', 'I', 'Θ', 'N', 'J'] as const;
export type BaseDimension = (typeof BASE_DIMENSIONS)[number];

const DIMENSION_LABELS: Record<BaseDimension, string> = {
  L: '长度',
  M: '质量',
  T: '时间',
  I: '电流',
  Θ: '热力学温度',
  N: '物质的量',
  J: '发光强度',
};

export class DimensionVector {
  readonly exponents: readonly number[];

  constructor(exponents: readonly number[]) {
    if (exponents.length !== BASE_DIMENSIONS.length) {
      throw new RangeError(`量纲向量长度必须为 ${BASE_DIMENSIONS.length}`);
    }
    for (const e of exponents) {
      if (!Number.isSafeInteger(e)) throw new RangeError(`量纲指数必须是整数: ${e}`);
    }
    this.exponents = Object.freeze([...exponents]);
  }

  static base(dim: BaseDimension, exponent = 1): DimensionVector {
    const exponents = new Array<number>(BASE_DIMENSIONS.length).fill(0);
    exponents[BASE_DIMENSIONS.indexOf(dim)] = exponent;
    return new DimensionVector(exponents);
  }

  static dimensionless(): DimensionVector {
    return new DimensionVector(new Array<number>(BASE_DIMENSIONS.length).fill(0));
  }

  mul(other: DimensionVector): DimensionVector {
    return new DimensionVector(this.exponents.map((e, i) => e + other.exponents[i]));
  }

  div(other: DimensionVector): DimensionVector {
    return new DimensionVector(this.exponents.map((e, i) => e - other.exponents[i]));
  }

  pow(n: number): DimensionVector {
    return new DimensionVector(this.exponents.map((e) => e * n));
  }

  equals(other: DimensionVector): boolean {
    return this.exponents.every((e, i) => e === other.exponents[i]);
  }

  isDimensionless(): boolean {
    return this.exponents.every((e) => e === 0);
  }

  /** 用作 Map 键的稳定字符串。 */
  get key(): string {
    return this.exponents.join(',');
  }

  toString(): string {
    const parts: string[] = [];
    this.exponents.forEach((e, i) => {
      if (e === 0) return;
      const symbol = BASE_DIMENSIONS[i];
      parts.push(e === 1 ? symbol : `${symbol}^${e}`);
    });
    return parts.length === 0 ? '1' : parts.join('·');
  }

  describe(): string {
    const parts: string[] = [];
    this.exponents.forEach((e, i) => {
      if (e !== 0) parts.push(`${DIMENSION_LABELS[BASE_DIMENSIONS[i]]}^${e}`);
    });
    return parts.length === 0 ? '无量纲' : parts.join('·');
  }
}
