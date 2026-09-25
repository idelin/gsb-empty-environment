/**
 * Versioned color spaces used for distance measurement.
 *
 * The version string is recorded in every serializable artifact (diffusion
 * frontier snapshots, quantize reports) so a restored run can prove it is
 * measuring error in the same space it was started with.
 */

export interface ColorSpace {
  /** Stable identifier, e.g. "srgb-u8-v1". Bumped on any semantic change. */
  readonly version: string;
  /** Map an sRGB u8 triple into the working space. */
  toSpace(r: number, g: number, b: number): [number, number, number];
  /** Squared distance between two points already in the working space. */
  dist2(a: readonly number[], b: readonly number[]): number;
}

function euclid2(a: readonly number[], b: readonly number[]): number {
  const dx = a[0] - b[0];
  const dy = a[1] - b[1];
  const dz = a[2] - b[2];
  return dx * dx + dy * dy + dz * dz;
}

/** Plain Euclidean distance on gamma-encoded sRGB bytes. */
export const SRGB_U8_V1: ColorSpace = {
  version: "srgb-u8-v1",
  toSpace: (r, g, b) => [r, g, b],
  dist2: euclid2,
};

function linearize(c: number): number {
  const s = c / 255;
  return s <= 0.04045 ? s / 12.92 : Math.pow((s + 0.055) / 1.055, 2.4);
}

/** Euclidean distance in linearized sRGB (IEC 61966-2-1 EOTF). */
export const SRGB_LINEAR_V1: ColorSpace = {
  version: "srgb-linear-v1",
  toSpace: (r, g, b) => [linearize(r), linearize(g), linearize(b)],
  dist2: euclid2,
};

const REGISTRY: ReadonlyMap<string, ColorSpace> = new Map([
  [SRGB_U8_V1.version, SRGB_U8_V1],
  [SRGB_LINEAR_V1.version, SRGB_LINEAR_V1],
]);

export function getColorSpace(version: string): ColorSpace {
  const cs = REGISTRY.get(version);
  if (!cs) {
    throw new Error(`unknown color space version: ${version}`);
  }
  return cs;
}
