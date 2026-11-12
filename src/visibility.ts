import { DEFAULT_EPSILON, distance, segmentBlockedByObstacles } from "./geometry.ts";
import { assignObstacleVertexIds, isPointObject, normalizeObstacles, normalizePoint } from "./input.ts";
import type {
  PlannerOptions,
  Point,
  Polygon,
  VisibilityEdge,
  VisibilityGraph,
  VisibilityNode,
} from "./types.ts";

export interface BuildVisibilityGraphInput {
  start: unknown;
  end?: unknown;
  goal?: unknown;
  obstacles?: unknown;
  options?: PlannerOptions;
}

function isBuildInput(value: unknown): value is BuildVisibilityGraphInput {
  if (!value || typeof value !== "object" || isPointObject(value)) {
    return false;
  }
  const objectValue = value as Record<string, unknown>;
  return objectValue.start !== undefined || objectValue.end !== undefined || objectValue.goal !== undefined;
}

function buildGraphFromParts(
  startInput: unknown,
  goalInput: unknown,
  obstaclesInput: unknown,
  options: PlannerOptions = {},
): VisibilityGraph {
  const epsilon = options.epsilon ?? DEFAULT_EPSILON;
  const startVertex = normalizePoint(startInput, "start");
  const goalVertex = normalizePoint(goalInput, "goal");
  const obstacles: Polygon[] = assignObstacleVertexIds(normalizeObstacles(obstaclesInput));

  const nodes: VisibilityNode[] = [
    {
      index: 0,
      x: startVertex.x,
      y: startVertex.y,
      id: startVertex.id ?? "start",
      kind: "start",
    },
    {
      index: 1,
      x: goalVertex.x,
      y: goalVertex.y,
      id: goalVertex.id ?? "goal",
      kind: "goal",
    },
  ];

  for (const obstacle of obstacles) {
    for (const vertex of obstacle.vertices) {
      nodes.push({
        index: nodes.length,
        x: vertex.x,
        y: vertex.y,
        id: vertex.id ?? nodes.length,
        kind: "obstacle",
        obstacleIndex: vertex.obstacleIndex,
        vertexIndex: vertex.vertexIndex,
      });
    }
  }

  const edges: VisibilityEdge[] = [];
  const adjacency: Array<Array<{ to: number; length: number }>> = nodes.map(() => []);

  for (let from = 0; from < nodes.length; from += 1) {
    for (let to = from + 1; to < nodes.length; to += 1) {
      const length = distance(nodes[from], nodes[to]);
      if (length <= epsilon) {
        continue;
      }
      if (segmentBlockedByObstacles(nodes[from], nodes[to], obstacles, epsilon)) {
        continue;
      }
      edges.push({ from, to, length });
      adjacency[from].push({ to, length });
      adjacency[to].push({ to: from, length });
    }
  }

  return {
    nodes,
    edges,
    adjacency,
    startIndex: 0,
    goalIndex: 1,
  };
}

export function buildVisibilityGraph(input: BuildVisibilityGraphInput): VisibilityGraph;
export function buildVisibilityGraph(
  start: unknown,
  goal: unknown,
  obstacles?: unknown,
  options?: PlannerOptions,
): VisibilityGraph;
export function buildVisibilityGraph(
  startOrInput: unknown,
  goal?: unknown,
  obstacles?: unknown,
  options: PlannerOptions = {},
): VisibilityGraph {
  if (isBuildInput(startOrInput)) {
    const input = startOrInput;
    return buildGraphFromParts(input.start, input.goal ?? input.end, input.obstacles, input.options ?? {});
  }
  return buildGraphFromParts(startOrInput, goal, obstacles, options);
}

export function nodesToPoints(nodes: readonly VisibilityNode[]): Point[] {
  return nodes.map((node) => ({ x: node.x, y: node.y }));
}
