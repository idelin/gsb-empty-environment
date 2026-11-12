export * from "./types.ts";
export * from "./errors.ts";
export * from "./affine.ts";
export * from "./geometry.ts";
export * from "./input.ts";
export * from "./visibility.ts";
export * from "./planner.ts";
export * from "./injector.ts";

import {
  TrajectoryRemapper,
  computeDragPath,
  findVisibilityPath,
  planDrag,
  planPath,
  planDragPath,
  remapAndPlan,
  remapAndPlanTrajectory,
  remapDrag,
  remapDragTrajectory,
  remapTrajectory,
  remapTrajectorySafe,
  shortestPath,
  shortestPathOnVisibilityGraph,
  tryRemapTrajectory,
} from "./planner.ts";
import {
  RecordingInjector,
  createDragSession,
  createRecordingInjector,
  executeDrag,
  executeDragOperation,
  executeDragPath,
  executeDragSync,
  executeDragTrajectory,
  performDrag,
  remapAndExecute,
  runDrag,
  startDrag,
} from "./injector.ts";
import { buildVisibilityGraph } from "./visibility.ts";
import { isSegmentCollisionFree } from "./geometry.ts";
import { DragCanceledError, TrajectoryRemapError } from "./errors.ts";

const api = {
  TrajectoryRemapError,
  DragCanceledError,
  TrajectoryRemapper,
  remapTrajectory,
  remapAndPlanTrajectory,
  remapAndPlan,
  remapDrag,
  remapDragTrajectory,
  remapTrajectorySafe,
  tryRemapTrajectory,
  planDrag,
  planPath,
  planDragPath,
  computeDragPath,
  shortestPath,
  shortestPathOnVisibilityGraph,
  findVisibilityPath,
  buildVisibilityGraph,
  isSegmentCollisionFree,
  executeDrag,
  executeDragOperation,
  executeDragPath,
  executeDragSync,
  executeDragTrajectory,
  performDrag,
  runDrag,
  remapAndExecute,
  createDragSession,
  createRecordingInjector,
  RecordingInjector,
  startDrag,
};

export default api;
