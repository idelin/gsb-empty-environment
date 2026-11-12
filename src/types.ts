export interface Point {
  x: number;
  y: number;
}

export interface Vertex extends Point {
  id?: string | number;
  obstacleIndex?: number;
  vertexIndex?: number;
}

export type PointLike =
  | Point
  | readonly [number, number]
  | readonly number[];

export interface Polygon {
  id?: string | number;
  vertices: Vertex[];
}

/**
 * The six values use the same convention as DOMMatrix/SVG:
 * x' = a*x + c*y + e, y' = b*x + d*y + f.
 */
export interface AffineMatrix {
  a: number;
  b: number;
  c: number;
  d: number;
  e: number;
  f: number;
}

export interface PlannerOptions {
  /**
   * Absolute geometry tolerance. Boundary contact is always allowed.
   */
  epsilon?: number;
  /**
   * Only vertex-id tie breaking is required, but accepting a custom comparator
   * makes the package easy to embed in a host that has its own stable IDs.
   */
  compareVertexIds?: (a: string | number, b: string | number) => number;
  /**
   * Include every visibility-graph vertex used by the chosen path in the result.
   * The default is true so callers can audit the vertex-id tie break.
   */
  keepCollinearWaypoints?: boolean;
}

export interface RemapTrajectoryInput {
  trajectory?: unknown;
  points?: unknown;
  mappedPoints?: unknown;
  recordedTrajectory?: unknown;
  recordedPoints?: unknown;
  recorded?: unknown;

  transform?: unknown;
  affine?: unknown;
  affineTransform?: unknown;
  matrix?: unknown;
  transformMatrix?: unknown;
  coordinateTransform?: unknown;
  windowTransform?: unknown;
  currentWindowTransform?: unknown;
  recordedToCurrentTransform?: unknown;
  recordedToCurrent?: unknown;
  recordedToTarget?: unknown;
  sourceToTarget?: unknown;
  coordinateMapping?: unknown;
  recordedTransform?: unknown;
  currentTransform?: unknown;
  recordedMatrix?: unknown;
  currentMatrix?: unknown;
  current?: unknown;
  from?: unknown;
  to?: unknown;

  region?: unknown;
  targetRegion?: unknown;
  draggableRegion?: unknown;
  dropRegion?: unknown;
  dragRegion?: unknown;
  allowedRegion?: unknown;
  dragArea?: unknown;
  targetArea?: unknown;
  validRegion?: unknown;

  obstacles?: unknown;
  obstaclePolygons?: unknown;
  blockedPolygons?: unknown;
  blockedAreas?: unknown;
  obstacleShapes?: unknown;
  blockers?: unknown;

  options?: PlannerOptions;
}

export interface Waypoint extends Point {
  id: string | number;
  kind: "start" | "goal" | "obstacle";
  obstacleIndex?: number;
  vertexIndex?: number;
}

export interface PathCost {
  bends: number;
  length: number;
  segments: number;
  vertexIds: Array<string | number>;
}

export interface RemapTrajectoryResult {
  ok: true;
  trajectory: Point[];
  mappedTrajectory: Point[];
  mappedPoints: Point[];
  path: Point[];
  plannedPath: Point[];
  start: Point;
  end: Point;
  goal: Point;
  bends: number;
  bendCount: number;
  length: number;
  distance: number;
  segments: number;
  links: number;
  vertexIds: Array<string | number>;
  pathIds: Array<string | number>;
  waypoints: Waypoint[];
  cost: PathCost;
  transform: AffineMatrix;
  region: Polygon;
  obstacles: Polygon[];
}

export interface VisibilityNode extends Waypoint {
  index: number;
}

export interface VisibilityEdge {
  from: number;
  to: number;
  length: number;
}

export interface VisibilityGraph {
  nodes: VisibilityNode[];
  edges: VisibilityEdge[];
  adjacency: Array<Array<{ to: number; length: number }>>;
  startIndex: number;
  goalIndex: number;
}

export interface PlanPathResult {
  ok: true;
  path: Point[];
  start: Point;
  end: Point;
  goal: Point;
  bends: number;
  bendCount: number;
  length: number;
  distance: number;
  segments: number;
  links: number;
  vertexIds: Array<string | number>;
  pathIds: Array<string | number>;
  waypoints: Waypoint[];
  cost: PathCost;
  graph: VisibilityGraph;
}

export interface DragEventRecord {
  type: "move" | "mousedown" | "mouseup" | "cancel";
  x?: number;
  y?: number;
  point?: Point;
  index?: number;
}

// eslint-disable-next-line @typescript-eslint/no-explicit-any
export type InjectorMethod = (...args: any[]) => any;

export interface PointerInjectorLike {
  moveTo?: InjectorMethod;
  move?: InjectorMethod;
  mouseMove?: InjectorMethod;
  pointerMove?: InjectorMethod;
  mouseDown?: InjectorMethod;
  down?: InjectorMethod;
  pointerDown?: InjectorMethod;
  press?: InjectorMethod;
  mouseUp?: InjectorMethod;
  up?: InjectorMethod;
  pointerUp?: InjectorMethod;
  release?: InjectorMethod;
  cancel?: InjectorMethod;
  emit?: InjectorMethod;
  send?: InjectorMethod;
  dispatch?: InjectorMethod;
  [key: string]: unknown;
}

export interface ExecuteDragOptions {
  signal?: AbortSignal;
  /**
   * Defaults to true (standard abort semantics). With false, a canceled drag
   * resolves to a result whose `canceled` flag is true after releasing.
   */
  throwOnCancel?: boolean;
  /**
   * Defaults to true. The pointer is moved to the first path point before the
   * single mouse-down event.
   */
  moveToStart?: boolean;
}

export interface ExecuteDragResult {
  ok: true;
  path: Point[];
  moved: number;
  mousedownCount: number;
  mouseupCount: number;
  canceled: boolean;
  events: DragEventRecord[];
}

export interface SafeRemapFailure {
  ok: false;
  error: Error;
  code: string;
}

export type SafeRemapResult = RemapTrajectoryResult | SafeRemapFailure;
