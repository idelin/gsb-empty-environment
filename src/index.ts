export type { Point, Rect } from "./geometry.ts";
export {
  point,
  rect,
  rectFromSize,
  rectWidth,
  rectHeight,
  rectArea,
  isEmptyRect,
  rectContainsPoint,
  rectContainsRect,
  intersectRect,
  subtractRect,
  pointsAlmostEqual,
  rectsAlmostEqual,
} from "./geometry.ts";
export type { Affine } from "./matrix.ts";
export {
  IDENTITY,
  applyAffine,
  composeAffine,
  invertAffine,
  mapRectAffine,
  affineAlmostEqual,
} from "./matrix.ts";
export type { Step, StepName, ClipAnalysis, RoundTripReport } from "./pipeline.ts";
export { TransformPipeline } from "./pipeline.ts";
export type { IntPoint, IntRect, RectRasterizeMode } from "./rasterize.ts";
export { rasterizePoint, rasterizeRect } from "./rasterize.ts";
