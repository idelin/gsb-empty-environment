import { inputError, TrajectoryRemapError } from "./errors.ts";
import { isFiniteNumber, distance } from "./geometry.ts";
import type { Point, Polygon, Vertex } from "./types.ts";

function record(value: unknown): Record<string, unknown> | undefined {
  return typeof value === "object" && value !== null ? (value as Record<string, unknown>) : undefined;
}

export function isPointObject(value: unknown): boolean {
  const objectValue = record(value);
  return Boolean(objectValue && isFiniteNumber(objectValue.x) && isFiniteNumber(objectValue.y));
}

export function isPointLike(value: unknown): boolean {
  if (isPointObject(value)) {
    return true;
  }
  return Array.isArray(value) && value.length >= 2 && isFiniteNumber(value[0]) && isFiniteNumber(value[1]);
}

export function normalizePoint(value: unknown, label = "point"): Vertex {
  if (Array.isArray(value)) {
    if (value.length < 2 || !isFiniteNumber(value[0]) || !isFiniteNumber(value[1])) {
      throw inputError(`${label} must be [x, y]`);
    }
    const result: Vertex = { x: value[0], y: value[1] };
    if (value.length >= 3 && (typeof value[2] === "string" || typeof value[2] === "number")) {
      result.id = value[2];
    }
    return result;
  }

  const objectValue = record(value);
  if (!objectValue || !isFiniteNumber(objectValue.x) || !isFiniteNumber(objectValue.y)) {
    throw inputError(`${label} must contain finite x and y coordinates`);
  }

  const result: Vertex = { x: objectValue.x, y: objectValue.y };
  const id = objectValue.id ?? objectValue.vertexId ?? objectValue.name ?? objectValue.key;
  if (typeof id === "string" || typeof id === "number") {
    result.id = id;
  }
  return result;
}

function polygonArray(value: unknown, label: string): unknown[] {
  if (Array.isArray(value)) {
    return value;
  }
  const objectValue = record(value);
  if (!objectValue) {
    return [];
  }

  for (const key of ["vertices", "points", "polygon", "ring", "path"]) {
    const candidate = objectValue[key];
    if (Array.isArray(candidate)) {
      return candidate;
    }
  }
  return [];
}

export function normalizePolygon(value: unknown, label = "polygon"): Polygon {
  const flatCoordinateList = Array.isArray(value) && value.length >= 6 && value.every(isFiniteNumber);
  if (!flatCoordinateList && isPointLike(value)) {
    throw inputError(`${label} must be a polygon, not a single point`);
  }

  const rawVertices = polygonArray(value, label);
  if (rawVertices.length === 0) {
    throw inputError(`${label} must contain a non-empty array of vertices`);
  }

  let vertices: Vertex[];
  if (rawVertices.every(isFiniteNumber)) {
    // A flat coordinate list such as [x0, y0, x1, y1, ...].
    const flat = rawVertices as unknown[];
    if (flat.length % 2 !== 0) {
      throw inputError(`${label} contains malformed vertices`);
    }
    vertices = [];
    for (let index = 0; index < flat.length; index += 2) {
      vertices.push({ x: flat[index] as number, y: flat[index + 1] as number });
    }
  } else {
    vertices = rawVertices.map((point, index) => normalizePoint(point, `${label}[${index}]`));
  }

  const cleaned: Vertex[] = [];
  for (const vertex of vertices) {
    const previous = cleaned[cleaned.length - 1];
    if (previous && distance(previous, vertex) <= 1e-12) {
      continue;
    }
    cleaned.push(vertex);
  }
  if (cleaned.length > 2 && distance(cleaned[0], cleaned[cleaned.length - 1]) <= 1e-12) {
    cleaned.pop();
  }

  if (cleaned.length < 3) {
    throw inputError(`${label} must have at least three distinct vertices`);
  }

  const objectValue = record(value);
  const polygon: Polygon = { vertices: cleaned };
  if (objectValue && (typeof objectValue.id === "string" || typeof objectValue.id === "number")) {
    polygon.id = objectValue.id;
  }
  return polygon;
}

function rectanglePolygon(value: Record<string, unknown>, label: string): Polygon | undefined {
  let left: number | undefined;
  let top: number | undefined;
  let right: number | undefined;
  let bottom: number | undefined;

  const x = value.x;
  const y = value.y;
  const width = value.width ?? value.w;
  const height = value.height ?? value.h;
  if (isFiniteNumber(x) && isFiniteNumber(y) && isFiniteNumber(width) && isFiniteNumber(height)) {
    left = Math.min(x, x + width);
    right = Math.max(x, x + width);
    top = Math.min(y, y + height);
    bottom = Math.max(y, y + height);
  } else if (
    isFiniteNumber(value.left) &&
    isFiniteNumber(value.top) &&
    isFiniteNumber(value.right) &&
    isFiniteNumber(value.bottom)
  ) {
    left = Math.min(value.left, value.right);
    right = Math.max(value.left, value.right);
    top = Math.min(value.top, value.bottom);
    bottom = Math.max(value.top, value.bottom);
  } else if (
    isFiniteNumber(value.minX) &&
    isFiniteNumber(value.minY) &&
    isFiniteNumber(value.maxX) &&
    isFiniteNumber(value.maxY)
  ) {
    left = Math.min(value.minX, value.maxX);
    right = Math.max(value.minX, value.maxX);
    top = Math.min(value.minY, value.maxY);
    bottom = Math.max(value.minY, value.maxY);
  } else if (
    isFiniteNumber(value.x1) &&
    isFiniteNumber(value.y1) &&
    isFiniteNumber(value.x2) &&
    isFiniteNumber(value.y2)
  ) {
    left = Math.min(value.x1, value.x2);
    right = Math.max(value.x1, value.x2);
    top = Math.min(value.y1, value.y2);
    bottom = Math.max(value.y1, value.y2);
  }

  if (left === undefined || top === undefined || right === undefined || bottom === undefined) {
    return undefined;
  }
  if (right - left <= 0 || bottom - top <= 0) {
    throw inputError(`${label} rectangle must have positive width and height`);
  }

  const polygon: Polygon = {
    vertices: [
      { x: left, y: top },
      { x: right, y: top },
      { x: right, y: bottom },
      { x: left, y: bottom },
    ],
  };
  if (typeof value.id === "string" || typeof value.id === "number") {
    polygon.id = value.id;
  }
  return polygon;
}

export function normalizeRegion(value: unknown, label = "target region"): Polygon {
  if (value === undefined || value === null) {
    throw inputError(`${label} is required`);
  }

  if (Array.isArray(value) && value.length === 4 && value.every(isFiniteNumber)) {
    const [x, y, width, height] = value as number[];
    return normalizeRegion({ x, y, width, height }, label);
  }

  const objectValue = record(value);
  if (objectValue && !polygonArray(value, label).length) {
    const rectangle = rectanglePolygon(objectValue, label);
    if (rectangle) {
      return rectangle;
    }
  }

  return normalizePolygon(value, label);
}

export function normalizeTrajectoryPoints(value: unknown, label = "recorded trajectory"): Vertex[] {
  let source = value;
  const objectValue = record(value);
  if (objectValue && !Array.isArray(value)) {
    for (const key of ["points", "samples", "trajectory", "path", "recordedTrajectory", "recordedPoints"]) {
      if (Array.isArray(objectValue[key])) {
        source = objectValue[key];
        break;
      }
    }
  }

  if (!Array.isArray(source)) {
    throw inputError(`${label} must be an array of points`);
  }
  if (source.length === 0) {
    throw new TrajectoryRemapError("EMPTY_TRAJECTORY", `${label} must contain at least one point`);
  }
  return source.map((point, index) => normalizePoint(point, `${label}[${index}]`));
}

function looksLikePolygonWrapper(value: unknown): boolean {
  const objectValue = record(value);
  if (!objectValue) {
    return false;
  }
  return ["vertices", "points", "polygon", "ring", "path"].some((key) => Array.isArray(objectValue[key]));
}

export function normalizeObstacles(value: unknown): Polygon[] {
  if (value === undefined || value === null) {
    return [];
  }

  const objectValue = record(value);
  if (objectValue && !isPointObject(value)) {
    for (const key of ["polygons", "obstacles", "obstaclePolygons", "items"]) {
      if (Array.isArray(objectValue[key])) {
        return normalizeObstacles(objectValue[key]);
      }
    }
    if (looksLikePolygonWrapper(value)) {
      return [normalizePolygon(objectValue, "obstacle")];
    }
  }

  if (Array.isArray(value)) {
    if (value.length === 0) {
      return [];
    }

    if (value.every(isFiniteNumber)) {
      return [normalizePolygon(value, "obstacle")];
    }

    if (
      value.every(
        (item) =>
          Array.isArray(item) &&
          item.length >= 4 &&
          item.every(isFiniteNumber),
      )
    ) {
      return value.map((polygon, index) => normalizePolygon(polygon, `obstacle[${index}]`));
    }

    if (value.every(isPointLike)) {
      return [normalizePolygon(value, "obstacle")];
    }

    return value.map((polygon, index) => normalizePolygon(polygon, `obstacle[${index}]`));
  }

  return [normalizePolygon(value, "obstacle")];
}

export function assignObstacleVertexIds(obstacles: Polygon[]): Polygon[] {
  let nextId = 0;
  return obstacles.map((polygon, obstacleIndex) => ({
    ...polygon,
    vertices: polygon.vertices.map((vertex, vertexIndex) => {
      const assignedId = vertex.id === undefined ? nextId : vertex.id;
      const result: Vertex = {
        x: vertex.x,
        y: vertex.y,
        id: assignedId,
        obstacleIndex,
        vertexIndex,
      };
      nextId += 1;
      return result;
    }),
  }));
}

export function pointFromVertex(vertex: Vertex): Point {
  return { x: vertex.x, y: vertex.y };
}
