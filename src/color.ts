/**
 * 颜色模型与合成规则（题内线性规则，不调用任何图形库混合）：
 *
 * - 内部表示：精确预乘颜色，r/g/b/a 均为 [0,1] 区间内的 Frac。
 * - 合成算子：Porter-Duff "over"，对各通道线性：
 *     out.c = top.c + bottom.c * (1 - top.a)   （c 含 r/g/b/a 四个通道）
 * - 输入格式：非预乘（straight）与预乘（premultiplied）8 位 RGBA 均可。
 * - 零透明度特判：
 *     1) straight 输入 a=0 时颜色通道无意义，统一归为全透明 (0,0,0,0)；
 *     2) premultiplied 输入 a=0 时要求颜色通道全 0，否则视为非法输入；
 *     3) 输出反预乘时精确 alpha 为 0（或量化为 0）则颜色通道输出 0，避免 0/0。
 * - 量化：round-half-up 到最近的 8 位整数。
 */

import { Frac } from "./fraction.ts";

export interface RGBA8 {
  r: number;
  g: number;
  b: number;
  a: number;
}

/** 精确预乘颜色，各通道为 [0,1] 内的有理数 */
export interface ExactColor {
  r: Frac;
  g: Frac;
  b: Frac;
  a: Frac;
}

export const TRANSPARENT: ExactColor = {
  r: Frac.ZERO,
  g: Frac.ZERO,
  b: Frac.ZERO,
  a: Frac.ZERO,
};

function checkByte(v: number, name: string): void {
  if (!Number.isInteger(v) || v < 0 || v > 255) {
    throw new RangeError(`${name} must be an integer in [0, 255], got ${v}`);
  }
}

function checkRGBA8(c: RGBA8): void {
  checkByte(c.r, "r");
  checkByte(c.g, "g");
  checkByte(c.b, "b");
  checkByte(c.a, "a");
}

/** 非预乘 8 位 -> 精确预乘。a=0 特判：颜色通道丢弃，归为全透明。 */
export function fromStraight8(c: RGBA8): ExactColor {
  checkRGBA8(c);
  if (c.a === 0) return TRANSPARENT;
  const a = Frac.of(c.a, 255);
  return {
    r: Frac.of(c.r, 255).mul(a),
    g: Frac.of(c.g, 255).mul(a),
    b: Frac.of(c.b, 255).mul(a),
    a,
  };
}

/** 预乘 8 位 -> 精确预乘。校验通道不超过 alpha；a=0 时通道必须全 0。 */
export function fromPremultiplied8(c: RGBA8): ExactColor {
  checkRGBA8(c);
  if (c.a === 0) {
    if (c.r !== 0 || c.g !== 0 || c.b !== 0) {
      throw new RangeError(
        `premultiplied color with a=0 must have zero channels, got ${JSON.stringify(c)}`,
      );
    }
    return TRANSPARENT;
  }
  if (c.r > c.a || c.g > c.a || c.b > c.a) {
    throw new RangeError(
      `premultiplied channel exceeds alpha: ${JSON.stringify(c)}`,
    );
  }
  return {
    r: Frac.of(c.r, 255),
    g: Frac.of(c.g, 255),
    b: Frac.of(c.b, 255),
    a: Frac.of(c.a, 255),
  };
}

/** Porter-Duff over：top 覆盖 bottom，线性逐通道。 */
export function over(top: ExactColor, bottom: ExactColor): ExactColor {
  const t = Frac.ONE.sub(top.a);
  return {
    r: top.r.add(bottom.r.mul(t)),
    g: top.g.add(bottom.g.mul(t)),
    b: top.b.add(bottom.b.mul(t)),
    a: top.a.add(bottom.a.mul(t)),
  };
}

/** 自底向上依次 over。layers[0] 为最底层。 */
export function composeOver(layersBottomToTop: ExactColor[]): ExactColor {
  let acc = TRANSPARENT;
  for (const layer of layersBottomToTop) acc = over(layer, acc);
  return acc;
}

function clamp01(x: Frac): Frac {
  if (x.sign() < 0) return Frac.ZERO;
  if (x.cmp(Frac.ONE) > 0) return Frac.ONE;
  return x;
}

/** round-half-up(x * 255)，x 先夹取到 [0,1]。 */
export function roundHalfUpByte(x: Frac): number {
  const v = clamp01(x);
  const q = (v.n * 255n * 2n + v.d) / (v.d * 2n);
  return Number(q);
}

/** 精确预乘 -> 非预乘 8 位。零透明度特判：颜色通道输出 0。 */
export function toStraight8(c: ExactColor): RGBA8 {
  const a = roundHalfUpByte(c.a);
  if (c.a.isZero() || a === 0) return { r: 0, g: 0, b: 0, a: 0 };
  const un = (ch: Frac): Frac => ch.div(c.a);
  return {
    r: roundHalfUpByte(un(c.r)),
    g: roundHalfUpByte(un(c.g)),
    b: roundHalfUpByte(un(c.b)),
    a,
  };
}

/** 精确预乘 -> 预乘 8 位。各通道独立量化后夹取到 alpha，保持 c<=a 不变式。 */
export function toPremultiplied8(c: ExactColor): RGBA8 {
  const a = roundHalfUpByte(c.a);
  if (c.a.isZero() || a === 0) return { r: 0, g: 0, b: 0, a: 0 };
  const ch = (x: Frac): number => Math.min(roundHalfUpByte(x), a);
  return { r: ch(c.r), g: ch(c.g), b: ch(c.b), a };
}
