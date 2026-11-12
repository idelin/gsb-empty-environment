import {
  applyAffineMatrix,
  invertAffineMatrix,
  normalizeAffineTransform,
} from "./affine.ts";
import { inputError, TrajectoryRemapError } from "./errors.ts";
import { DEFAULT_EPSILON, distance, pointLocation } from "./geometry.ts";
import {
  assignObstacleVertexIds,
  normalizeObstacles,
  normalizePoint,
  normalizeRegion,
  normalizeTrajectoryPoints,
} from "./input.ts";
import { buildVisibilityGraph } from "./visibility.ts";
import type {
  PathCost,
  PlannerOptions,
  PlanPathResult,
  Point,
  Polygon,
  RemapTrajectoryInput,
  RemapTrajectoryResult,
  VisibilityGraph,
  VisibilityNode,
  Waypoint,
} from "./types.ts";

interface SearchLabel {
  node: number;
  bends: number;
  length: number;
  path: number[];
  ids: Array<string | number>;
  lastUx: number | null;
  lastUy: number | null;
}

interface QueueItem {
  label: SearchLabel;
  index: number;
}

class MinHeap {
  items: QueueItem[];
  compare: (left: QueueItem, right: QueueItem) => number;

  constructor(compare: (left: QueueItem, right: QueueItem) => number) {
    this.items = [];
    this.compare = compare;
  }

  get size(): number {
    return this.items.length;
  }

  push(item: QueueItem): void {
    const items = this.items;
    items.push(item);
    let index = items.length - 1;
    while (index > 0) {
      const parent = (index - 1) >> 1;
      if (this.compare(items[index], items[parent]) >= 0) {
        break;
      }
      [items[index], items[parent]] = [items[parent], items[index]];
      index = parent;
    }
  }

  pop(): QueueItem | undefined {
    const items = this.items;
    if (items.length === 0) {
      return undefined;
    }

    const result = items[0];
    const last = items.pop() as QueueItem;
    if (items.length > 0) {
      items[0] = last;
      let index = 0;
      while (true) {
        const left = index * 2 + 1;
        const right = left + 1;
        let smallest = index;
        if (left < items.length && this.compare(items[left], items[smallest]) < 0) {
          smallest = left;
        }
        if (right < items.length && this.compare(items[right], items[smallest]) < 0) {
          smallest = right;
        }
        if (smallest === index) {
          break;
        }
        [items[index], items[smallest]] = [items[smallest], items[index]];
        index = smallest;
      }
    }
    return result;
  }
}

function defaultCompareIds(left: string | number, right: string | number): number {
  if (typeof left === "number" && typeof right === "number") {
    return left - right;
  }
  if (typeof left === "string" && typeof right === "string") {
    return left < right ? -1 : left > right ? 1 : 0;
  }
  const leftText = String(left);
  const rightText = String(right);
  return leftText < rightText ? -1 : leftText > rightText ? 1 : 0;
}

function compareIdSequences(
  left: Array<string | number>,
  right: Array<string | number>,
  compareIds: (a: string | number, b: string | number) => number,
): number {
  const length = Math.min(left.length, right.length);
  for (let index = 0; index < length; index += 1) {
    const comparison = compareIds(left[index], right[index]);
    if (comparison !== 0) {
      return comparison;
    }
  }
  return left.length - right.length;
}

function compareLabels(
  left: SearchLabel,
  right: SearchLabel,
  lengthTolerance: number,
  compareIds: (a: string | number, b: string | number) => number,
): number {
  if (left.bends !== right.bends) {
    return left.bends - right.bends;
  }

  const lengthDelta = left.length - right.length;
  if (Math.abs(lengthDelta) > lengthTolerance) {
    return lengthDelta;
  }

  // Start and goal are fixed for both paths. Comparing only the intervening
  // obstacle vertices makes a direct edge (empty sequence) win a tie over a
  // collinear detour through obstacle vertices.
  const leftInterior = left.ids.length > 2 ? left.ids.slice(1, -1) : [];
  const rightInterior = right.ids.length > 2 ? right.ids.slice(1, -1) : [];
  const idComparison = compareIdSequences(leftInterior, rightInterior, compareIds);
  if (idComparison !== 0) {
    return idComparison;
  }
  return lengthDelta;
}

function sameDirection(label: SearchLabel, ux: number, uy: number, tolerance: number): boolean {
  if (label.lastUx === null || label.lastUy === null) {
    return false;
  }
  const crossValue = label.lastUx * uy - label.lastUy * ux;
  const dotValue = label.lastUx * ux + label.lastUy * uy;
  return Math.abs(crossValue) <= tolerance && dotValue > 0;
}

function findStateIndex(labels: SearchLabel[], ux: number, uy: number, tolerance: number): number {
  for (let index = 0; index < labels.length; index += 1) {
    if (sameDirection(labels[index], ux, uy, tolerance)) {
      return index;
    }
  }
  return -1;
}

function simplifyCollinear(path: number[], graph: VisibilityGraph, epsilon: number): number[] {
  if (path.length <= 2) {
    return path;
  }

  const result = [path[0]];
  let previousDirection: { x: number; y: number } | null = null;
  for (let index = 1; index < path.length; index += 1) {
    const fromNode = graph.nodes[path[index - 1]];
    const toNode = graph.nodes[path[index]];
    const dx = toNode.x - fromNode.x;
    const dy = toNode.y - fromNode.y;
    const length = Math.hypot(dx, dy);
    const direction = length > 0 ? { x: dx / length, y: dy / length } : null;

    if (
      previousDirection &&
      direction &&
      Math.abs(previousDirection.x * direction.y - previousDirection.y * direction.x) <= epsilon &&
      previousDirection.x * direction.x + previousDirection.y * direction.y > 0
    ) {
      result[result.length - 1] = path[index];
    } else {
      result.push(path[index]);
    }
    previousDirection = direction;
  }
  return result;
}

function waypointsFromPath(path: number[], graph: VisibilityGraph): Waypoint[] {
  return path.map((nodeIndex) => {
    const node = graph.nodes[nodeIndex];
    const waypoint: Waypoint = {
      x: node.x,
      y: node.y,
      id: node.id,
      kind: node.kind,
    };
    if (node.obstacleIndex !== undefined) {
      waypoint.obstacleIndex = node.obstacleIndex;
    }
    if (node.vertexIndex !== undefined) {
      waypoint.vertexIndex = node.vertexIndex;
    }
    return waypoint;
  });
}

function pathPointsFromWaypoints(waypoints: Waypoint[]): Point[] {
  return waypoints.map((waypoint) => {
    const point: Point & { id?: string | number } = { x: waypoint.x, y: waypoint.y };
    // Keep the public shape backward-compatible with plain {x, y} points while
    // still letting hosts inspect the winning vertex ID directly.
    Object.defineProperty(point, "id", {
      value: waypoint.id,
      enumerable: false,
      configurable: true,
    });
    return point;
  });
}

function makeCost(waypoints: Waypoint[], bends: number, length: number): PathCost {
  return {
    bends,
    length,
    segments: Math.max(0, waypoints.length - 1),
    vertexIds: waypoints.map((waypoint) => waypoint.id),
  };
}

function findGraphPath(
  graph: VisibilityGraph,
  options: PlannerOptions,
): { path: number[]; bends: number; length: number } | undefined {
  const epsilon = options.epsilon ?? DEFAULT_EPSILON;
  const lengthTolerance = Math.max(epsilon, 1e-12);
  const compareIds = options.compareVertexIds ?? defaultCompareIds;
  const directionTolerance = Math.max(epsilon, 1e-10);

  const labels: SearchLabel[][] = graph.nodes.map(() => []);
  const queue = new MinHeap((left, right) => compareLabels(left.label, right.label, lengthTolerance, compareIds));
  const startLabel: SearchLabel = {
    node: graph.startIndex,
    bends: 0,
    length: 0,
    path: [graph.startIndex],
    ids: [graph.nodes[graph.startIndex].id],
    lastUx: null,
    lastUy: null,
  };
  labels[graph.startIndex].push(startLabel);
  queue.push({ label: startLabel, index: 0 });

  while (queue.size > 0) {
    const current = queue.pop() as QueueItem;
    const nodeLabels = labels[current.label.node];
    if (nodeLabels[current.index] !== current.label) {
      continue;
    }

    if (current.label.node === graph.goalIndex) {
      return {
        path: current.label.path,
        bends: current.label.bends,
        length: current.label.length,
      };
    }

    const fromNode = graph.nodes[current.label.node];
    for (const edge of graph.adjacency[current.label.node]) {
      const toNode = graph.nodes[edge.to];
      const dx = toNode.x - fromNode.x;
      const dy = toNode.y - fromNode.y;
      const edgeLength = edge.length;
      if (edgeLength <= epsilon) {
        continue;
      }

      const ux = dx / edgeLength;
      const uy = dy / edgeLength;
      const continues = sameDirection(current.label, ux, uy, directionTolerance);
      const nextBends = current.label.lastUx === null ? 0 : current.label.bends + (continues ? 0 : 1);
      const nextLength = current.label.length + edgeLength;
      const nextLabel: SearchLabel = {
        node: edge.to,
        bends: nextBends,
        length: nextLength,
        path: [...current.label.path, edge.to],
        ids: [...current.label.ids, toNode.id],
        lastUx: ux,
        lastUy: uy,
      };

      const targetLabels = labels[edge.to];
      const stateIndex = findStateIndex(targetLabels, ux, uy, directionTolerance);
      if (stateIndex >= 0) {
        if (compareLabels(nextLabel, targetLabels[stateIndex], lengthTolerance, compareIds) < 0) {
          targetLabels[stateIndex] = nextLabel;
          queue.push({ label: nextLabel, index: stateIndex });
        }
      } else {
        targetLabels.push(nextLabel);
        queue.push({ label: nextLabel, index: targetLabels.length - 1 });
      }
    }
  }

  return undefined;
}

export interface PlanPathInput {
  start: unknown;
  end?: unknown;
  goal?: unknown;
  obstacles?: unknown;
  options?: PlannerOptions;
}

function isPlanObject(value: unknown): boolean {
  if (!value || typeof value !== "object" || Array.isArray(value)) {
    return false;
  }
  const objectValue = value as Record<string, unknown>;
  return objectValue.start !== undefined || objectValue.end !== undefined || objectValue.goal !== undefined;
}

function planPathFromParts(
  startInput: unknown,
  goalInput: unknown,
  obstaclesInput: unknown,
  options: PlannerOptions = {},
): PlanPathResult {
  const epsilon = options.epsilon ?? DEFAULT_EPSILON;
  const start = normalizePoint(startInput, "start");
  const end = normalizePoint(goalInput, "goal");
  const obstacles = assignObstacleVertexIds(normalizeObstacles(obstaclesInput));

  for (const obstacle of obstacles) {
    if (pointLocation(obstacle, start, epsilon) === "inside") {
      throw new TrajectoryRemapError("START_INSIDE_OBSTACLE", "Drag start lies inside an obstacle");
    }
    if (pointLocation(obstacle, end, epsilon) === "inside") {
      throw new TrajectoryRemapError("END_INSIDE_OBSTACLE", "Drag end lies inside an obstacle");
    }
  }

  const graph = buildVisibilityGraph({
    start,
    goal: end,
    obstacles,
    options: { ...options, epsilon },
  });

  if (distance(start, end) <= epsilon) {
    const waypoints = waypointsFromPath([graph.startIndex, graph.goalIndex], graph);
    const cost = makeCost(waypoints, 0, 0);
    return {
      ok: true,
      path: pathPointsFromWaypoints(waypoints),
      start: { x: start.x, y: start.y },
      end: { x: end.x, y: end.y },
      goal: { x: end.x, y: end.y },
      bends: 0,
      bendCount: 0,
      length: 0,
      distance: 0,
      segments: cost.segments,
      links: cost.segments,
      vertexIds: cost.vertexIds,
      pathIds: cost.vertexIds,
      waypoints,
      cost,
      graph,
    };
  }

  const result = findGraphPath(graph, options);
  if (!result) {
    throw new TrajectoryRemapError("NO_PATH", "No collision-free visibility-graph path exists", {
      start: { x: start.x, y: start.y },
      end: { x: end.x, y: end.y },
    });
  }

  let path = result.path;
  if (options.keepCollinearWaypoints === false) {
    path = simplifyCollinear(path, graph, Math.max(epsilon, 1e-12));
  }

  const waypoints = waypointsFromPath(path, graph);
  const cost = makeCost(waypoints, result.bends, result.length);
  return {
    ok: true,
    path: pathPointsFromWaypoints(waypoints),
    start: { x: start.x, y: start.y },
    end: { x: end.x, y: end.y },
    goal: { x: end.x, y: end.y },
    bends: result.bends,
    bendCount: result.bends,
    length: result.length,
    distance: result.length,
    segments: cost.segments,
    links: cost.segments,
    vertexIds: cost.vertexIds,
    pathIds: cost.vertexIds,
    waypoints,
    cost,
    graph,
  };
}

export function shortestPathOnVisibilityGraph(
  graph: VisibilityGraph,
  options: PlannerOptions = {},
): PlanPathResult {
  const epsilon = options.epsilon ?? DEFAULT_EPSILON;
  const startNode = graph.nodes[graph.startIndex];
  const goalNode = graph.nodes[graph.goalIndex];
  if (!startNode || !goalNode) {
    throw new TrajectoryRemapError("INVALID_INPUT", "Visibility graph has no start/goal nodes");
  }

  if (distance(startNode, goalNode) <= epsilon) {
    const waypoints = waypointsFromPath([graph.startIndex, graph.goalIndex], graph);
    const cost = makeCost(waypoints, 0, 0);
    return {
      ok: true,
      path: pathPointsFromWaypoints(waypoints),
      start: { x: startNode.x, y: startNode.y },
      end: { x: goalNode.x, y: goalNode.y },
      goal: { x: goalNode.x, y: goalNode.y },
      bends: 0,
      bendCount: 0,
      length: 0,
      distance: 0,
      segments: cost.segments,
      links: cost.segments,
      vertexIds: cost.vertexIds,
      pathIds: cost.vertexIds,
      waypoints,
      cost,
      graph,
    };
  }

  const result = findGraphPath(graph, options);
  if (!result) {
    throw new TrajectoryRemapError("NO_PATH", "No collision-free visibility-graph path exists");
  }

  let path = result.path;
  if (options.keepCollinearWaypoints === false) {
    path = simplifyCollinear(path, graph, Math.max(epsilon, 1e-12));
  }
  const waypoints = waypointsFromPath(path, graph);
  const cost = makeCost(waypoints, result.bends, result.length);
  return {
    ok: true,
    path: pathPointsFromWaypoints(waypoints),
    start: { x: startNode.x, y: startNode.y },
    end: { x: goalNode.x, y: goalNode.y },
    goal: { x: goalNode.x, y: goalNode.y },
    bends: result.bends,
    bendCount: result.bends,
    length: result.length,
    distance: result.length,
    segments: cost.segments,
    links: cost.segments,
    vertexIds: cost.vertexIds,
    pathIds: cost.vertexIds,
    waypoints,
    cost,
    graph,
  };
}

export function findVisibilityPath(
  graph: VisibilityGraph,
  options?: PlannerOptions,
): PlanPathResult {
  return shortestPathOnVisibilityGraph(graph, options);
}

export function shortestPath(graph: VisibilityGraph, options?: PlannerOptions): PlanPathResult;
export function shortestPath(
  start: unknown,
  goal: unknown,
  obstacles?: unknown,
  options?: PlannerOptions,
): PlanPathResult;
export function shortestPath(
  graphOrStart: unknown,
  goalOrOptions?: unknown,
  obstacles?: unknown,
  options: PlannerOptions = {},
): PlanPathResult {
  if (
    graphOrStart &&
    typeof graphOrStart === "object" &&
    Array.isArray((graphOrStart as Record<string, unknown>).nodes) &&
    Array.isArray((graphOrStart as Record<string, unknown>).adjacency)
  ) {
    const graphOptions =
      goalOrOptions && typeof goalOrOptions === "object"
        ? (goalOrOptions as PlannerOptions)
        : {};
    return shortestPathOnVisibilityGraph(graphOrStart as VisibilityGraph, graphOptions);
  }
  return planPath(graphOrStart, goalOrOptions, obstacles, options);
}

export function planPath(input: PlanPathInput): PlanPathResult;
export function planPath(
  start: unknown,
  goal: unknown,
  obstacles?: unknown,
  options?: PlannerOptions,
): PlanPathResult;
export function planPath(
  startOrInput: unknown,
  goal?: unknown,
  obstacles?: unknown,
  options: PlannerOptions = {},
): PlanPathResult {
  if (isPlanObject(startOrInput)) {
    const input = startOrInput as PlanPathInput;
    return planPathFromParts(input.start, input.goal ?? input.end, input.obstacles, input.options ?? {});
  }
  return planPathFromParts(startOrInput, goal, obstacles, options);
}

function remapInputFromObject(input: RemapTrajectoryInput): {
  trajectory: unknown;
  transform: unknown;
  region: unknown;
  obstacles: unknown;
  options: PlannerOptions;
} {
  if (!input || typeof input !== "object") {
    throw inputError("remapTrajectory expects an input object or positional arguments");
  }

  const trajectory =
    input.trajectory ??
    input.points ??
    input.mappedPoints ??
    input.recordedPoints ??
    input.recordedTrajectory ??
    (Array.isArray(input.recorded) ? input.recorded : undefined);

  let transform =
    input.transform ??
    input.affine ??
    input.affineTransform ??
    input.matrix ??
    input.transformMatrix ??
    input.coordinateTransform ??
    input.windowTransform ??
    input.currentWindowTransform ??
    input.recordedToCurrentTransform ??
    input.recordedToCurrent ??
    input.recordedToTarget ??
    input.sourceToTarget ??
    input.coordinateMapping;

  if (transform === undefined) {
    if (
      input.recorded !== undefined &&
      input.current !== undefined &&
      !Array.isArray(input.recorded) &&
      !Array.isArray(input.current)
    ) {
      transform = { recorded: input.recorded, current: input.current };
    }
  }

  if (transform === undefined) {
    if (
      input.recordedTransform !== undefined ||
      input.currentTransform !== undefined ||
      input.recordedMatrix !== undefined ||
      input.currentMatrix !== undefined ||
      input.from !== undefined ||
      input.to !== undefined
    ) {
      transform = {
        recordedTransform: input.recordedTransform ?? input.recordedMatrix ?? input.from,
        currentTransform: input.currentTransform ?? input.currentMatrix ?? input.to,
      };
    }
  }

  const region =
    input.region ??
    input.targetRegion ??
    input.draggableRegion ??
    input.dropRegion ??
    input.dragRegion ??
    input.allowedRegion ??
    input.dragArea ??
    input.targetArea ??
    input.validRegion;
  const obstacles =
    input.obstacles ??
    input.obstaclePolygons ??
    input.blockedPolygons ??
    input.blockedAreas ??
    input.obstacleShapes ??
    input.blockers;
  return {
    trajectory,
    transform,
    region,
    obstacles,
    options: input.options ?? {},
  };
}

function remapFromParts(
  trajectoryInput: unknown,
  transformInput: unknown,
  regionInput: unknown,
  obstaclesInput: unknown,
  options: PlannerOptions = {},
): RemapTrajectoryResult {
  const epsilon = options.epsilon ?? DEFAULT_EPSILON;
  const recordedPoints = normalizeTrajectoryPoints(trajectoryInput);
  const transform = normalizeAffineTransform(transformInput);
  const region = normalizeRegion(regionInput);
  const obstacles = assignObstacleVertexIds(normalizeObstacles(obstaclesInput));

  const mappedTrajectory = recordedPoints.map((point) => applyAffineMatrix(transform, point));
  const start = mappedTrajectory[0];
  const end = mappedTrajectory[mappedTrajectory.length - 1];

  const startLocation = pointLocation(region, start, epsilon);
  if (startLocation === "outside") {
    throw new TrajectoryRemapError("START_OUTSIDE_REGION", "Mapped drag start is outside the target region", {
      start,
      region,
    });
  }
  const endLocation = pointLocation(region, end, epsilon);
  if (endLocation === "outside") {
    throw new TrajectoryRemapError("END_OUTSIDE_REGION", "Mapped drag end is outside the target region", {
      end,
      region,
    });
  }

  for (const obstacle of obstacles) {
    if (pointLocation(obstacle, start, epsilon) === "inside") {
      throw new TrajectoryRemapError("START_INSIDE_OBSTACLE", "Mapped drag start lies inside an obstacle");
    }
    if (pointLocation(obstacle, end, epsilon) === "inside") {
      throw new TrajectoryRemapError("END_INSIDE_OBSTACLE", "Mapped drag end lies inside an obstacle");
    }
  }

  const planned = planPathFromParts(start, end, obstacles, options);
  return {
    ok: true,
    trajectory: mappedTrajectory,
    mappedTrajectory,
    mappedPoints: mappedTrajectory,
    path: planned.path,
    plannedPath: planned.path,
    start: planned.start,
    end: planned.end,
    goal: planned.goal,
    bends: planned.bends,
    bendCount: planned.bends,
    length: planned.length,
    distance: planned.length,
    segments: planned.segments,
    links: planned.segments,
    vertexIds: planned.vertexIds,
    pathIds: planned.vertexIds,
    waypoints: planned.waypoints,
    cost: planned.cost,
    transform,
    region,
    obstacles,
  };
}

export function remapTrajectory(input: RemapTrajectoryInput): RemapTrajectoryResult;
export function remapTrajectory(
  trajectory: unknown,
  transform: unknown,
  region: unknown,
  obstacles?: unknown,
  options?: PlannerOptions,
): RemapTrajectoryResult;
export function remapTrajectory(
  trajectoryOrInput: unknown,
  transform?: unknown,
  region?: unknown,
  obstacles?: unknown,
  options: PlannerOptions = {},
): RemapTrajectoryResult {
  if (
    transform === undefined &&
    region === undefined &&
    !Array.isArray(trajectoryOrInput) &&
    typeof trajectoryOrInput === "object" &&
    trajectoryOrInput !== null
  ) {
    const objectValue = trajectoryOrInput as Record<string, unknown>;
    if (
      objectValue.trajectory !== undefined ||
      objectValue.points !== undefined ||
      objectValue.mappedPoints !== undefined ||
      objectValue.recordedTrajectory !== undefined ||
      objectValue.recordedPoints !== undefined ||
      objectValue.region !== undefined ||
      objectValue.targetRegion !== undefined ||
      objectValue.draggableRegion !== undefined ||
      objectValue.dropRegion !== undefined ||
      objectValue.dragRegion !== undefined ||
      objectValue.allowedRegion !== undefined ||
      objectValue.dragArea !== undefined ||
      objectValue.targetArea !== undefined ||
      objectValue.validRegion !== undefined
    ) {
      const parts = remapInputFromObject(trajectoryOrInput as RemapTrajectoryInput);
      return remapFromParts(parts.trajectory, parts.transform, parts.region, parts.obstacles, parts.options);
    }
  }
  return remapFromParts(trajectoryOrInput, transform, region, obstacles, options);
}

export function remapTrajectorySafe(input: RemapTrajectoryInput): RemapTrajectoryResult | {
  ok: false;
  error: Error;
  code: string;
};
export function remapTrajectorySafe(
  trajectory: unknown,
  transform: unknown,
  region: unknown,
  obstacles?: unknown,
  options?: PlannerOptions,
): RemapTrajectoryResult | { ok: false; error: Error; code: string };
export function remapTrajectorySafe(
  trajectoryOrInput: unknown,
  transform?: unknown,
  region?: unknown,
  obstacles?: unknown,
  options?: PlannerOptions,
): RemapTrajectoryResult | {
  ok: false;
  error: Error;
  code: string;
} {
  try {
    return remapTrajectory(trajectoryOrInput as RemapTrajectoryInput, transform, region, obstacles, options);
  } catch (error) {
    const caught = error instanceof Error ? error : new Error(String(error));
    return {
      ok: false,
      error: caught,
      code: caught instanceof TrajectoryRemapError ? caught.code : "INVALID_INPUT",
    };
  }
}

export function tryRemapTrajectory(input: RemapTrajectoryInput) {
  return remapTrajectorySafe(input);
}

export function remapAndPlanTrajectory(input: RemapTrajectoryInput): RemapTrajectoryResult {
  return remapTrajectory(input);
}

export function remapAndPlan(
  trajectory: unknown,
  transform: unknown,
  region: unknown,
  obstacles?: unknown,
  options?: PlannerOptions,
): RemapTrajectoryResult {
  return remapTrajectory(trajectory, transform, region, obstacles, options);
}

export function planDragPath(input: PlanPathInput): PlanPathResult;
export function planDragPath(
  start: unknown,
  goal: unknown,
  obstacles?: unknown,
  options?: PlannerOptions,
): PlanPathResult;
export function planDragPath(
  startOrInput: unknown,
  goal?: unknown,
  obstacles?: unknown,
  options: PlannerOptions = {},
): PlanPathResult {
  return planPath(startOrInput, goal, obstacles, options);
}

export function findShortestPath(
  start: unknown,
  goal: unknown,
  obstacles?: unknown,
  options?: PlannerOptions,
): PlanPathResult {
  return planPath(start, goal, obstacles, options);
}

export function computeDragPath(
  start: unknown,
  goal: unknown,
  obstacles?: unknown,
  options?: PlannerOptions,
): PlanPathResult {
  return planPath(start, goal, obstacles, options);
}

export function planDrag(
  startOrInput: unknown,
  goal?: unknown,
  obstacles?: unknown,
  options?: PlannerOptions,
): PlanPathResult {
  return planPath(startOrInput, goal, obstacles, options);
}

export function remapDragTrajectory(input: RemapTrajectoryInput): RemapTrajectoryResult {
  return remapTrajectory(input);
}

export function remapDrag(
  trajectoryOrInput: unknown,
  transform?: unknown,
  region?: unknown,
  obstacles?: unknown,
  options?: PlannerOptions,
): RemapTrajectoryResult {
  return remapTrajectory(
    trajectoryOrInput as RemapTrajectoryInput,
    transform,
    region,
    obstacles,
    options,
  );
}

export function mapTrajectoryWithTransform(
  trajectory: unknown,
  transform: unknown,
): Point[] {
  const points = normalizeTrajectoryPoints(trajectory);
  const matrix = normalizeAffineTransform(transform);
  return points.map((point) => applyAffineMatrix(matrix, point));
}

export function invertTransform(transform: unknown) {
  return invertAffineMatrix(normalizeAffineTransform(transform));
}

export class TrajectoryRemapper {
  options: PlannerOptions;

  constructor(options: PlannerOptions = {}) {
    this.options = options;
  }

  remap(input: RemapTrajectoryInput): RemapTrajectoryResult {
    return remapTrajectory({
      ...input,
      options: { ...this.options, ...(input.options ?? {}) },
    });
  }

  plan(input: PlanPathInput): PlanPathResult {
    return planPath({
      ...input,
      options: { ...this.options, ...(input.options ?? {}) },
    });
  }

  map(trajectory: unknown, transform: unknown): Point[] {
    return mapTrajectoryWithTransform(trajectory, transform);
  }
}

export { TrajectoryRemapper as DragTrajectoryPlanner };
export { TrajectoryRemapper as DragTrajectoryRemapper };
