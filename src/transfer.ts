/**
 * Transfer functions are represented separately from matrices.
 * All of them are bijective over the whole real line: negative linear
 * components follow the sign-mirrored definition (extended sRGB,
 * IEC 61966-2-1 style), so they never produce NaN.
 */
export interface TransferFunction {
  readonly id: string;
  /** linear -> encoded (non-linear) */
  encode(linear: number): number;
  /** encoded -> linear */
  decode(encoded: number): number;
}

function signPow(x: number, p: number): number {
  return Math.sign(x) * Math.pow(Math.abs(x), p);
}

export const LINEAR: TransferFunction = {
  id: 'linear',
  encode: (v) => v,
  decode: (v) => v,
};

export function gammaTransfer(gamma: number): TransferFunction {
  if (!Number.isFinite(gamma) || gamma <= 0) {
    throw new Error(`invalid gamma: ${gamma}`);
  }
  return {
    id: `gamma(${gamma})`,
    encode: (v) => signPow(v, 1 / gamma),
    decode: (v) => signPow(v, gamma),
  };
}

export const SRGB: TransferFunction = {
  id: 'srgb',
  encode: (v) => {
    const a = Math.abs(v);
    const enc = a <= 0.0031308 ? 12.92 * a : 1.055 * Math.pow(a, 1 / 2.4) - 0.055;
    return Math.sign(v) * enc;
  },
  decode: (v) => {
    const a = Math.abs(v);
    const dec = a <= 0.04045 ? a / 12.92 : Math.pow((a + 0.055) / 1.055, 2.4);
    return Math.sign(v) * dec;
  },
};
