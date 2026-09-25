export { GenerationCache, type BorrowHandle, type CacheEntry } from "./cache.ts";
export { ControllableScheduler, type ScheduledTask } from "./scheduler.ts";
export {
  FontMeasurer,
  FontRegistry,
  shape,
  sizeofClusters,
  sizeofFontFace,
  sizeofMeasurement,
  type MeasureHandle,
  type MeasureRequest,
} from "./measurer.ts";
export { LayoutEngine, layoutNode, type LayoutFeedback, type LayoutSession } from "./layout.ts";
export * from "./types.ts";
export * as fixtures from "./fixtures.ts";
