export { Dem } from "./dem.ts";
export { RainfallSeries } from "./rainfall.ts";
export { FloodSimulator, type SimulatorOptions } from "./simulator.ts";
export { connectedRegions, boundaryPolygons, analyzeMask } from "./regions.ts";
export { RoadNetwork, rasterizeRoad, rasterizeSegment, type Road } from "./roads.ts";
export type {
  Grid2D,
  Mask2D,
  CellCoord,
  Point,
  Polygon,
  BreakpointSegment,
} from "./types.ts";
