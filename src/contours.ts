import { rgbaToMask, type ForegroundOptions, type RgbaImage } from "./image.ts";

export interface Point {
  x: number;
  y: number;
}

export type ContourKind = "outer" | "hole";

export interface Contour {
  id: number;
  kind: ContourKind;
  vertices: Point[];
  edges: Array<{ from: Point; to: Point }>;
  signedArea: number;
}

const EAST = 0;
const SOUTH = 1;
const WEST = 2;
const NORTH = 3;

const DX = [1, 0, -1, 0] as const;
const DY = [0, 1, 0, -1] as const;

const DIRECTION_NAME = ["east", "south", "west", "north"] as const;

interface DirectedEdge {
  fromVertex: number;
  toVertex: number;
  direction: number;
}

function vertexId(x: number, y: number, stride: number): number {
  return y * stride + x;
}

function pointOf(vertex: number, stride: number): Point {
  return {
    x: vertex % stride,
    y: Math.floor(vertex / stride),
  };
}

function edgeKey(vertex: number, direction: number): number {
  return vertex * 4 + direction;
}

function makeDirectedEdge(fromX: number, fromY: number, direction: number, stride: number): DirectedEdge {
  const fromVertex = vertexId(fromX, fromY, stride);
  const toVertex = vertexId(fromX + DX[direction], fromY + DY[direction], stride);
  return { fromVertex, toVertex, direction };
}

function collectBoundaryEdges(mask: Uint8Array, width: number, height: number): Map<number, DirectedEdge> {
  const stride = width + 1;
  const edges = new Map<number, DirectedEdge>();

  const foregroundAt = (x: number, y: number): boolean =>
    x >= 0 && x < width && y >= 0 && y < height && mask[y * width + x] === 1;

  const addEdge = (x: number, y: number, direction: number): void => {
    const edge = makeDirectedEdge(x, y, direction, stride);
    edges.set(edgeKey(edge.fromVertex, direction), edge);
  };

  for (let y = 0; y < height; y += 1) {
    for (let x = 0; x < width; x += 1) {
      if (!foregroundAt(x, y)) {
        continue;
      }

      if (!foregroundAt(x, y - 1)) {
        addEdge(x, y, EAST);
      }
      if (!foregroundAt(x + 1, y)) {
        addEdge(x + 1, y, SOUTH);
      }
      if (!foregroundAt(x, y + 1)) {
        addEdge(x + 1, y + 1, WEST);
      }
      if (!foregroundAt(x - 1, y)) {
        addEdge(x, y + 1, NORTH);
      }
    }
  }

  return edges;
}

function outgoingEdge(edges: Map<number, DirectedEdge>, vertex: number, incomingDirection: number): DirectedEdge | undefined {
  const rightTurn = (incomingDirection + 1) % 4;
  const straight = incomingDirection;
  const leftTurn = (incomingDirection + 3) % 4;

  return (
    edges.get(edgeKey(vertex, rightTurn)) ??
    edges.get(edgeKey(vertex, straight)) ??
    edges.get(edgeKey(vertex, leftTurn))
  );
}

function signedArea(vertices: Point[]): number {
  let twiceArea = 0;
  for (let i = 0; i < vertices.length; i += 1) {
    const current = vertices[i];
    const next = vertices[(i + 1) % vertices.length];
    twiceArea += current.x * next.y - next.x * current.y;
  }
  return twiceArea / 2;
}

function toContour(vertices: number[], stride: number, id: number): Contour {
  const points = vertices.map((vertex) => pointOf(vertex, stride));
  const area = signedArea(points);
  if (area === 0) {
    throw new Error("contour walk produced a zero-area cycle");
  }

  const edges = points.map((from, index) => ({
    from,
    to: points[(index + 1) % points.length],
  }));

  return {
    id,
    kind: area > 0 ? "outer" : "hole",
    vertices: points,
    edges,
    signedArea: area,
  };
}

export function extractContoursFromMask(mask: Uint8Array, width: number, height: number): Contour[] {
  if (mask.length !== width * height) {
    throw new Error("mask dimensions do not match");
  }

  const stride = width + 1;
  const edges = collectBoundaryEdges(mask, width, height);
  const visited = new Set<number>();
  const contours: Contour[] = [];

  for (const startEdge of edges.values()) {
    const startKey = edgeKey(startEdge.fromVertex, startEdge.direction);
    if (visited.has(startKey)) {
      continue;
    }

    let current = startEdge;
    const vertices = [current.fromVertex];
    const seenAtCycle = new Set<number>();

    while (!visited.has(edgeKey(current.fromVertex, current.direction))) {
      const currentKey = edgeKey(current.fromVertex, current.direction);
      visited.add(currentKey);
      if (seenAtCycle.has(currentKey)) {
        throw new Error(`contour walk repeated directed edge: ${DIRECTION_NAME[current.direction]}`);
      }
      seenAtCycle.add(currentKey);

      const next = outgoingEdge(edges, current.toVertex, current.direction);
      if (!next) {
        throw new Error("boundary edge chain is not closed");
      }
      if (next.fromVertex !== current.toVertex) {
        throw new Error("contour walk left the current boundary vertex");
      }

      const nextKey = edgeKey(next.fromVertex, next.direction);
      if (nextKey === startKey) {
        break;
      }
      vertices.push(next.fromVertex);
      current = next;
    }

    if (vertices.some((vertex, index) => vertices.indexOf(vertex) !== index)) {
      throw new Error("contour walk repeated a vertex before closing");
    }
    contours.push(toContour(vertices, stride, contours.length + 1));
  }

  contours.sort((a, b) => {
    const ay = a.vertices[0]?.y ?? Number.MAX_SAFE_INTEGER;
    const by = b.vertices[0]?.y ?? Number.MAX_SAFE_INTEGER;
    return ay - by || (a.vertices[0]?.x ?? Number.MAX_SAFE_INTEGER) - (b.vertices[0]?.x ?? 0);
  });
  return contours.map((contour, index) => ({ ...contour, id: index + 1 }));
}

export function extractContours(image: RgbaImage, options: ForegroundOptions = {}): Contour[] {
  const mask = rgbaToMask(image, options);
  return extractContoursFromMask(mask, image.width, image.height);
}

function windingAt(x: number, y: number, contours: readonly Contour[]): number {
  let winding = 0;

  for (const contour of contours) {
    for (const { from, to } of contour.edges) {
      if (from.y <= y) {
        if (to.y > y && (to.x - from.x) * (y - from.y) < (x - from.x) * (to.y - from.y)) {
          winding += 1;
        }
      } else if (
        to.y <= y &&
        (to.x - from.x) * (y - from.y) > (x - from.x) * (to.y - from.y)
      ) {
        winding -= 1;
      }
    }
  }

  return winding;
}

export function fillContours(contours: readonly Contour[], width: number, height: number): Uint8Array {
  if (!Number.isInteger(width) || width <= 0 || !Number.isInteger(height) || height <= 0) {
    throw new Error("width and height must be positive integers");
  }

  const mask = new Uint8Array(width * height);
  for (let y = 0; y < height; y += 1) {
    for (let x = 0; x < width; x += 1) {
      if (windingAt(x + 0.5, y + 0.5, contours) !== 0) {
        mask[y * width + x] = 1;
      }
    }
  }
  return mask;
}

export function masksEqual(left: Uint8Array, right: Uint8Array): boolean {
  if (left.length !== right.length) {
    return false;
  }
  return left.every((value, index) => value === right[index]);
}
