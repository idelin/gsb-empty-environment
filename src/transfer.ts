/**
 * Transfer functions are kept separate from matrices: a color space is
 * (primaries + white point) for the linear stage plus a transfer function
 * for the non-linear encoding stage.
 *
 * All transfer functions here are sign-preserving extensions of their
 * standard definitions: f(-x) = -f(x). Negative linear components (which
 * occur for out-of-gamut colors) are therefore handled per definition and
 * never produce NaN from fractional powers of negative bases.
 */
export interface TransferFunction {
  readonly id: string;
  /** encoded (non-linear) -> linear */
  decode(encoded: number): number;
  /** linear -> encoded (non-linear) */
  encode(linear: number): number;
}

export const linearTransfer: TransferFunction = {
  id: 'linear',
  decode(v) {
    return v;
  },
  encode(v) {
    return v;
  },
};

export const srgbTransfer: TransferFunction = {
  id: 'srgb',
  decode(c) {
    const a = Math.abs(c);
    const l = a <= 0.04045 ? a / 12.92 : Math.pow((a + 0.055) / 1.055, 2.4);
    return Math.sign(c) * l;
  },
  encode(l) {
    const a = Math.abs(l);
    const c = a <= 0.0031308 ? 12.92 * a : 1.055 * Math.pow(a, 1 / 2.4) - 0.055;
    return Math.sign(l) * c;
  },
};

export function gammaTransfer(gamma: number, id = `gamma-${gamma}`): TransferFunction {
  if (!(gamma > 0) || !Number.isFinite(gamma)) {
    throw new Error(`invalid gamma: ${gamma}`);
  }
  return {
    id,
    decode(c) {
      return Math.sign(c) * Math.pow(Math.abs(c), gamma);
    },
    encode(l) {
      return Math.sign(l) * Math.pow(Math.abs(l), 1 / gamma);
    },
  };
}
