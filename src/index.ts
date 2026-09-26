export {
  buildPalette,
  medianCut,
  solveExact,
  paletteSse,
  type BuildPaletteOptions,
} from "./palette.ts";
export {
  encodeIndexed,
  encodeRows,
  DiffusionEncoder,
  frontierFromJSON,
  residualAt,
  type EncodeOptions,
  type FullIndexedImage,
} from "./encoder.ts";
export { reconstruct, reconstructionError, isNearestAssignment } from "./reconstruction.ts";
export type { ReconstructionErrorReport } from "./reconstruction.ts";
export { revisePixels } from "./revision.ts";
export {
  COLOR_SPACE_VERSION,
  TRANSPARENT_ALPHA_THRESHOLD,
  makeImage,
  getPixel,
  setPixel,
  cloneImage,
  opacityMask,
  isOpaqueAt,
  distinctOpaqueColors,
} from "./image.ts";
export { nearestIndex, clampChannel } from "./color.ts";
export { canDeliver, FS_SCALE } from "./diffusion-core.ts";
export type {
  RgbaImage,
  Rgb,
  IndexedImage,
  FrontierState,
  PixelEdit,
  RevisionResult,
  QuantMethod,
  PaletteResult,
} from "./types.ts";
