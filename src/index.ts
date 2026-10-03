export { Graph } from "./graph.ts";
export {
  coreNumbers,
  coreLayers,
  kCoreNodes,
  kCoreSubgraph,
  maxCore,
} from "./core.ts";
export type { BatchStats, Subgraph } from "./core.ts";
export { CoreMaintenance } from "./incremental.ts";
export type { UpdateStats } from "./incremental.ts";
