import type { Point, Polygon, Vertex } from "./types.ts";

export const DEFAULT_EPSILON = 1e-9;

export type PointLocation = "inside" | "outside" | "boundary";

export function isFiniteNumber(value: unknown): value is number {
  return typeof value === "number" && Number.isFinite(value);
}

export function distance(a: Point, b: Point): number {
  return Math.hypot(b.x - a.x, b.y - a.y);
}

export function cross(ax: number, ay: number, bx: number, by: number): number {
  return ax * by - ay * bx;
}

export function dot(ax: number, ay: number, bx: number, by: number): number {
  return ax * bx + ay * by;
}

export function pointOnSegment(a: Point, b: Point, p: Point, epsilon = DEFAULT_EPSILON): boolean {
  const abx = b.x - a.x;
  const aby = b.y - a.y;
  const apx = p.x - a.x;
  const apy = p.y - a.y;
  const length = Math.hypot(abx, aby);

  if (length <= epsilon) {
    return Math.hypot(apx, apy) <= epsilon;
  }

  const crossDistance = Math.abs(cross(abx, aby, apx, apy));
  if (crossDistance > epsilon * Math.max(1, length)) {
    return false;
  }

  const projection = dot(apx, apy, abx, aby);
  const lengthSquared = length * length;
  const projectionTolerance = epsilon * Math.max(1, lengthSquared);
  return projection >= -projectionTolerance && projection <= lengthSquared + projectionTolerance;
}

export function pointLocation(polygon: Polygon, point: Point, epsilon = DEFAULT_EPSILON): PointLocation {
  const vertices = polygon.vertices;
  if (vertices.length < 3) {
    return "outside";
  }

  for (let index = 0; index < vertices.length; index += 1) {
    const a = vertices[index];
    const b = vertices[(index + 1) % vertices.length];
    if (pointOnSegment(a, b, point, epsilon)) {
      return "boundary";
    }
  }

  let inside = false;
  for (let index = 0; index < vertices.length; index += 1) {
    const a = vertices[index];
    const b = vertices[(index + 1) % vertices.length];
    const straddles = (a.y > point.y) !== (b.y > point.y);
    if (!straddles) {
      continue;
    }

    const dy = b.y - a.y;
    if (Math.abs(dy) <= epsilon) {
      continue;
    }
    const intersectionX = a.x + ((point.y - a.y) * (b.x - a.x)) / dy;
    if (point.x < intersectionX) {
      inside = !inside;
    }
  }

  return inside ? "inside" : "outside";
}

export function pointInsidePolygon(polygon: Polygon, point: Point, epsilon = DEFAULT_EPSILON): boolean {
  return pointLocation(polygon, point, epsilon) === "inside";
}

function addUnique(values: number[], value: number, epsilon: number): void {
  for (let index = 0; index < values.length; index += 1) {
    if (Math.abs(values[index] - value) <= epsilon) {
      return;
    }
  }
  values.push(value);
}

function collectBoundaryTimes(
  a: Point,
  b: Point,
  polygon: Polygon,
  epsilon: number,
): number[] {
  const times = [0, 1];
  const rX = b.x - a.x;
  const rY = b.y - a.y;
  const segmentLength = Math.hypot(rX, rY);
  if (segmentLength <= epsilon) {
    return times;
  }

  const vertices: Vertex[] = polygon.vertices;
  for (let index = 0; index < vertices.length; index += 1) {
    const c = vertices[index];
    const d = vertices[(index + 1) % vertices.length];
    const sX = d.x - c.x;
    const sY = d.y - c.y;
    const sLength = Math.hypot(sX, sY);
    if (sLength <= epsilon) {
      continue;
    }

    const qX = c.x - a.x;
    const qY = c.y - a.y;
    const denominator = cross(rX, rY, sX, sY);
    const parallelTolerance = epsilon * Math.max(1, segmentLength * sLength);

    if (Math.abs(denominator) > parallelTolerance) {
      const t = cross(qX, qY, sX, sY) / denominator;
      const u = cross(qX, qY, rX, rY) / denominator;
      const parameterTolerance = Math.max(epsilon, 1e-12);
      if (t >= -parameterTolerance && t <= 1 + parameterTolerance && u >= -parameterTolerance && u <= 1 + parameterTolerance) {
        addUnique(times, Math.min(1, Math.max(0, t)), Math.max(epsilon, 1e-12));
      }
      continue;
    }

    const collinearTolerance = epsilon * Math.max(1, segmentLength);
    if (Math.abs(cross(qX, qY, rX, rY)) > collinearTolerance) {
      continue;
    }

    // A boundary edge that overlaps the segment contributes its endpoints as
    // split parameters. This also handles a segment that lies exactly on an
    // obstacle edge (edge-hugging is allowed).
    for (const endpoint of [c, d]) {
      if (pointOnSegment(a, b, endpoint, epsilon)) {
        const t = dot(endpoint.x - a.x, endpoint.y - a.y, rX, rY) / (segmentLength * segmentLength);
        addUnique(times, Math.min(1, Math.max(0, t)), Math.max(epsilon, 1e-12));
      }
    }
  }

  times.sort((left, right) => left - right);
  return times;
}

/**
 * A segment is illegal only if an open interval of it lies strictly inside an
 * obstacle. Touching a vertex or travelling along an edge is legal.
 */
export function segmentEntersPolygonInterior(
  a: Point,
  b: Point,
  polygon: Polygon,
  epsilon = DEFAULT_EPSILON,
): boolean {
  if (pointInsidePolygon(polygon, a, epsilon) || pointInsidePolygon(polygon, b, epsilon)) {
    return true;
  }

  if (distance(a, b) <= epsilon) {
    return false;
  }

  const times = collectBoundaryTimes(a, b, polygon, epsilon);
  for (let index = 0; index + 1 < times.length; index += 1) {
    const from = times[index];
    const to = times[index + 1];
    if (to - from <= Math.max(epsilon, 1e-12)) {
      continue;
    }

    const middle = (from + to) / 2;
    const sample = {
      x: a.x + (b.x - a.x) * middle,
      y: a.y + (b.y - a.y) * middle,
    };
    if (pointInsidePolygon(polygon, sample, epsilon)) {
      return true;
    }
  }

  return false;
}

export function segmentBlockedByObstacles(
  a: Point,
  b: Point,
  obstacles: Polygon[],
  epsilon = DEFAULT_EPSILON,
): boolean {
  for (const obstacle of obstacles) {
    if (segmentEntersPolygonInterior(a, b, obstacle, epsilon)) {
      return true;
    }
  }
  return false;
}

export function isSegmentCollisionFree(
  a: Point,
  b: Point,
  obstacles: Polygon[],
  epsilon = DEFAULT_EPSILON,
): boolean {
  return !segmentBlockedByObstacles(a, b, obstacles, epsilon);
}

export function polygonPerimeter(polygon: Polygon): number {
  let total = 0;
  for (let index = 0; index < polygon.vertices.length; index += 1) {
    total += distance(polygon.vertices[index], polygon.vertices[(index + 1) % polygon.vertices.length]);
  }
  return total;
}
