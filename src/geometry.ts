import {
  abs,
  add,
  cmp,
  div,
  eq,
  isZero,
  mul,
  rational,
  rationalKey,
  sign,
  sub,
  ZERO,
} from "./rational.ts";
import type { Rational, RationalInput } from "./rational.ts";

export interface Point {
  readonly x: Rational;
  readonly y: Rational;
}

export interface PointInput {
  readonly x: RationalInput;
  readonly y: RationalInput;
}

export type PointLike = PointInput | readonly [RationalInput, RationalInput];
export type RingInput = readonly PointLike[];

export interface PolygonInput {
  readonly outer?: RingInput;
  readonly rings?: readonly RingInput[];
  readonly holes?: readonly RingInput[];
}

export type PolygonLike = PolygonInput | RingInput;

export interface RegionInputObject {
  readonly polygons: readonly PolygonLike[];
}

export interface Polygon {
  readonly outer: readonly Point[];
  readonly holes: readonly (readonly Point[])[];
}

export interface Region {
  readonly polygons: readonly Polygon[];
}

export type RegionLike =
  | Region
  | RegionInputObject
  | PolygonLike
  | readonly PolygonLike[];

interface Segment {
  readonly a: Point;
  readonly b: Point;
}

interface Halfedge {
  readonly id: number;
  readonly from: number;
  readonly to: number;
}

interface Arrangement {
  readonly vertices: readonly Point[];
  readonly segments: readonly Segment[];
  readonly halfedges: readonly Halfedge[];
  readonly twin: readonly number[];
  readonly outgoing: readonly (readonly number[])[];
  readonly position: readonly number[];
}

function point(value: PointLike): Point {
  if (Array.isArray(value)) {
    if (value.length !== 2) {
      throw new TypeError("A point tuple must have exactly two coordinates");
    }
    return Object.freeze({ x: rational(value[0]), y: rational(value[1]) });
  }
  const object = value as unknown as Record<string, unknown>;
  if (!Object.prototype.hasOwnProperty.call(object, "x") || !Object.prototype.hasOwnProperty.call(object, "y")) {
    throw new TypeError("A point must contain x and y rational coordinates");
  }
  return Object.freeze({
    x: rational(object.x as RationalInput),
    y: rational(object.y as RationalInput),
  });
}

function isPointLike(value: unknown): value is PointLike {
  if (Array.isArray(value)) {
    return value.length === 2 && !Array.isArray(value[0]) && !Array.isArray(value[1]);
  }
  if (typeof value !== "object" || value === null) {
    return false;
  }
  const object = value as Record<string, unknown>;
  return Object.prototype.hasOwnProperty.call(object, "x") && Object.prototype.hasOwnProperty.call(object, "y");
}

function isRingLike(value: unknown): value is readonly PointLike[] {
  return Array.isArray(value) && value.length > 0 && value.every((entry) => isPointLike(entry));
}

function pointKey(value: Point): string {
  return `${rationalKey(value.x)},${rationalKey(value.y)}`;
}

function pointsEqual(a: Point, b: Point): boolean {
  return eq(a.x, b.x) && eq(a.y, b.y);
}

function normalizeRing(input: RingInput): Point[] {
  if (!Array.isArray(input)) {
    throw new TypeError("A polygon ring must be an array of points");
  }
  const output: Point[] = [];
  for (const item of input) {
    const current = point(item);
    if (output.length === 0 || !pointsEqual(output[output.length - 1]!, current)) {
      output.push(current);
    }
  }
  if (output.length > 1 && pointsEqual(output[0]!, output[output.length - 1]!)) {
    output.pop();
  }
  if (output.length !== 0 && output.length < 3) {
    throw new TypeError("A polygon ring must contain at least three distinct vertices");
  }
  return output;
}

function ringSignedArea(ring: readonly Point[]): Rational {
  let twice = ZERO;
  for (let index = 0; index < ring.length; index += 1) {
    const a = ring[index]!;
    const b = ring[(index + 1) % ring.length]!;
    twice = add(twice, sub(mul(a.x, b.y), mul(b.x, a.y)));
  }
  return div(twice, 2n);
}

function makePolygon(outer: RingInput, holes: readonly RingInput[] = []): Polygon {
  return Object.freeze({
    outer: Object.freeze(normalizeRing(outer)),
    holes: Object.freeze(holes.map((hole) => Object.freeze(normalizeRing(hole)))),
  });
}

function pointOnRing(value: Point, ring: readonly Point[]): boolean {
  for (let index = 0; index < ring.length; index += 1) {
    if (onClosedSegment(value, ring[index]!, ring[(index + 1) % ring.length]!)) {
      return true;
    }
  }
  return false;
}

function ringInsideRing(candidate: readonly Point[], container: readonly Point[]): boolean {
  if (candidate.length === 0 || container.length < 3) {
    return false;
  }
  return candidate.every(
    (vertex) => pointOnRing(vertex, container) || pointInRing(vertex, container),
  );
}

function polygonFromRings(rings: readonly RingInput[]): Polygon[] {
  if (rings.length === 0) {
    return [];
  }
  if (rings.length === 1) {
    return [makePolygon(rings[0]!)];
  }
  const normalized = rings.map((ring) => normalizeRing(ring));
  const first = normalized[0]!;
  const allContained = normalized
    .slice(1)
    .every((ring) => ringInsideRing(ring, first));
  if (allContained) {
    return [
      Object.freeze({
        outer: Object.freeze(first),
        holes: Object.freeze(normalized.slice(1).map((hole) => Object.freeze(hole))),
      }),
    ];
  }
  return normalized.map((ring) => Object.freeze({ outer: Object.freeze(ring), holes: Object.freeze([]) }));
}

function normalizePolygon(input: PolygonLike): Polygon[] {
  if (Array.isArray(input)) {
    if (input.length === 0) {
      return [];
    }
    if (isPointLike(input[0])) {
      return [makePolygon(input as RingInput)];
    }
    return polygonFromRings(input as readonly RingInput[]);
  }
  const object = input as PolygonInput;
  if (object.outer !== undefined) {
    return [makePolygon(object.outer, object.holes ?? [])];
  }
  if (object.rings !== undefined) {
    if (object.rings.length === 0) {
      return [];
    }
    return [
      Object.freeze({
        outer: Object.freeze(normalizeRing(object.rings[0]!)),
        holes: Object.freeze(object.rings.slice(1).map((hole) => Object.freeze(normalizeRing(hole)))),
      }),
    ];
  }
  throw new TypeError("A polygon needs an outer ring or a rings array");
}

export function normalizeRegion(input: RegionLike): Region {
  if (input === null || input === undefined) {
    throw new TypeError("A synthetic region is required");
  }
  if (!Array.isArray(input) && typeof input === "object" && "polygons" in input) {
    const polygons = (input as RegionInputObject).polygons;
    if (!Array.isArray(polygons)) {
      throw new TypeError("Region polygons must be an array");
    }
    if (polygons.length > 0 && polygons.every((entry) => isRingLike(entry))) {
      return Object.freeze({
        polygons: Object.freeze(polygonFromRings(polygons as readonly RingInput[])),
      });
    }
    return Object.freeze({
      polygons: Object.freeze(polygons.flatMap((polygon) => normalizePolygon(polygon))),
    });
  }
  if (Array.isArray(input)) {
    if (input.length === 0) {
      return Object.freeze({ polygons: Object.freeze([]) });
    }
    if (isPointLike(input[0])) {
      return Object.freeze({
        polygons: Object.freeze([makePolygon(input as RingInput)]),
      });
    }
    if (input.every((entry) => isRingLike(entry))) {
      return Object.freeze({
        polygons: Object.freeze(polygonFromRings(input as readonly RingInput[])),
      });
    }
    return Object.freeze({
      polygons: Object.freeze(
        (input as readonly PolygonLike[]).flatMap((polygon) => normalizePolygon(polygon)),
      ),
    });
  }
  return Object.freeze({ polygons: Object.freeze(normalizePolygon(input as PolygonLike)) });
}

export function polygonRegion(outer: RingInput, holes: readonly RingInput[] = []): Region {
  return Object.freeze({ polygons: Object.freeze([makePolygon(outer, holes)]) });
}

export function regionOf(polygons: readonly { outer: RingInput; holes?: readonly RingInput[] }[]): Region {
  return Object.freeze({
    polygons: Object.freeze(
      polygons.map((entry) => makePolygon(entry.outer, entry.holes ?? [])),
    ),
  });
}

export function pointInRing(value: Point, ring: readonly Point[]): boolean {
  if (ring.length < 3) {
    return false;
  }
  let inside = false;
  for (let index = 0; index < ring.length; index += 1) {
    const a = ring[index]!;
    const b = ring[(index + 1) % ring.length]!;
    const crosses = (cmp(a.y, value.y) > 0) !== (cmp(b.y, value.y) > 0);
    if (!crosses) {
      continue;
    }
    const x = add(a.x, div(mul(sub(value.y, a.y), sub(b.x, a.x)), sub(b.y, a.y)));
    if (cmp(x, value.x) > 0) {
      inside = !inside;
    }
  }
  return inside;
}

export function pointInRegion(value: PointInput, regionInput: RegionLike): boolean {
  const normalized = normalizeRegion(regionInput);
  const current = point(value);
  return normalized.polygons.some(
    (polygon) =>
      pointInRing(current, polygon.outer) &&
      !polygon.holes.some((hole) => pointInRing(current, hole)),
  );
}

function cross(a: Point, b: Point): Rational {
  return sub(mul(a.x, b.y), mul(a.y, b.x));
}

function vector(a: Point, b: Point): Point {
  return Object.freeze({ x: sub(b.x, a.x), y: sub(b.y, a.y) });
}

function translated(value: Point, delta: Point, scale: Rational): Point {
  return Object.freeze({
    x: add(value.x, mul(delta.x, scale)),
    y: add(value.y, mul(delta.y, scale)),
  });
}

function onClosedSegment(value: Point, a: Point, b: Point): boolean {
  if (!isZero(cross(vector(a, b), vector(a, value)))) {
    return false;
  }
  const minX = cmp(a.x, b.x) <= 0 ? a.x : b.x;
  const maxX = cmp(a.x, b.x) <= 0 ? b.x : a.x;
  const minY = cmp(a.y, b.y) <= 0 ? a.y : b.y;
  const maxY = cmp(a.y, b.y) <= 0 ? b.y : a.y;
  return (
    cmp(value.x, minX) >= 0 &&
    cmp(value.x, maxX) <= 0 &&
    cmp(value.y, minY) >= 0 &&
    cmp(value.y, maxY) <= 0
  );
}

function intersectionParameters(a: Point, b: Point, c: Point, d: Point): {
  readonly t: Rational;
  readonly u: Rational;
} | null {
  const r = vector(a, b);
  const s = vector(c, d);
  const denominator = cross(r, s);
  if (isZero(denominator)) {
    return null;
  }
  const offset = vector(a, c);
  return Object.freeze({
    t: div(cross(offset, s), denominator),
    u: div(cross(offset, r), denominator),
  });
}

function withinUnit(value: Rational): boolean {
  return cmp(value, 0n) >= 0 && cmp(value, 1n) <= 0;
}

function projectionParameter(a: Point, b: Point, value: Point): Rational | null {
  const direction = vector(a, b);
  if (!isZero(cross(direction, vector(a, value)))) {
    return null;
  }
  if (!isZero(direction.x)) {
    return div(sub(value.x, a.x), direction.x);
  }
  if (!isZero(direction.y)) {
    return div(sub(value.y, a.y), direction.y);
  }
  return null;
}

function collectSegments(region: Region): Segment[] {
  const segments: Segment[] = [];
  for (const polygon of region.polygons) {
    for (const ring of [polygon.outer, ...polygon.holes]) {
      if (ring.length < 3) {
        continue;
      }
      for (let index = 0; index < ring.length; index += 1) {
        const a = ring[index]!;
        const b = ring[(index + 1) % ring.length]!;
        if (!pointsEqual(a, b)) {
          segments.push({ a, b });
        }
      }
    }
  }
  return segments;
}

function buildArrangement(segments: readonly Segment[]): Arrangement {
  const splits = segments.map(() => [ZERO, rational(1n)]);
  for (let left = 0; left < segments.length; left += 1) {
    const first = segments[left]!;
    for (let right = left + 1; right < segments.length; right += 1) {
      const second = segments[right]!;
      const hit = intersectionParameters(first.a, first.b, second.a, second.b);
      if (hit !== null) {
        if (withinUnit(hit.t) && withinUnit(hit.u)) {
          splits[left]!.push(hit.t);
          splits[right]!.push(hit.u);
        }
        continue;
      }
      if (!isZero(cross(vector(first.a, first.b), vector(first.a, second.a)))) {
        continue;
      }
      for (const endpoint of [second.a, second.b]) {
        const parameter = projectionParameter(first.a, first.b, endpoint);
        if (parameter !== null && withinUnit(parameter) && onClosedSegment(endpoint, first.a, first.b)) {
          splits[left]!.push(parameter);
        }
      }
      for (const endpoint of [first.a, first.b]) {
        const parameter = projectionParameter(second.a, second.b, endpoint);
        if (parameter !== null && withinUnit(parameter) && onClosedSegment(endpoint, second.a, second.b)) {
          splits[right]!.push(parameter);
        }
      }
    }
  }

  const vertexIndex = new Map<string, number>();
  const vertices: Point[] = [];
  const vertexId = (value: Point): number => {
    const key = pointKey(value);
    const found = vertexIndex.get(key);
    if (found !== undefined) {
      return found;
    }
    const created = vertices.length;
    vertices.push(value);
    vertexIndex.set(key, created);
    return created;
  };

  const atomic = new Map<string, Segment>();
  for (let index = 0; index < segments.length; index += 1) {
    const segment = segments[index]!;
    const direction = vector(segment.a, segment.b);
    const unique = new Map<string, Rational>();
    for (const parameter of splits[index]!) {
      unique.set(rationalKey(parameter), parameter);
    }
    const ordered = [...unique.values()].sort((a, b) => cmp(a, b));
    for (let position = 0; position < ordered.length - 1; position += 1) {
      const start = ordered[position]!;
      const end = ordered[position + 1]!;
      if (eq(start, end)) {
        continue;
      }
      const a = translated(segment.a, direction, start);
      const b = translated(segment.a, direction, end);
      if (pointsEqual(a, b)) {
        continue;
      }
      const firstId = vertexId(a);
      const secondId = vertexId(b);
      if (firstId === secondId) {
        continue;
      }
      const low = Math.min(firstId, secondId);
      const high = Math.max(firstId, secondId);
      const key = `${low}:${high}`;
      if (!atomic.has(key)) {
        atomic.set(
          key,
          firstId < secondId
            ? { a: vertices[firstId]!, b: vertices[secondId]! }
            : { a: vertices[secondId]!, b: vertices[firstId]! },
        );
      }
    }
  }

  const orderedSegments = [...atomic.values()];
  const halfedges: Halfedge[] = [];
  const twin: number[] = [];
  for (let index = 0; index < orderedSegments.length; index += 1) {
    const segment = orderedSegments[index]!;
    const from = vertexId(segment.a);
    const to = vertexId(segment.b);
    const forward = halfedges.length;
    const backward = forward + 1;
    halfedges.push({ id: forward, from, to });
    halfedges.push({ id: backward, from: to, to: from });
    twin[forward] = backward;
    twin[backward] = forward;
  }

  const outgoing: number[][] = vertices.map(() => []);
  for (const halfedge of halfedges) {
    outgoing[halfedge.from]!.push(halfedge.id);
  }

  const directionOf = (halfedge: Halfedge): Point =>
    vector(vertices[halfedge.from]!, vertices[halfedge.to]!);

  const halfOf = (value: Point): 0 | 1 =>
    cmp(value.y, 0n) > 0 || (isZero(value.y) && cmp(value.x, 0n) >= 0) ? 0 : 1;

  const angleCompare = (leftId: number, rightId: number): number => {
    const left = directionOf(halfedges[leftId]!);
    const right = directionOf(halfedges[rightId]!);
    const halfDifference = halfOf(left) - halfOf(right);
    if (halfDifference !== 0) {
      return halfDifference;
    }
    const turn = cross(left, right);
    if (!isZero(turn)) {
      return sign(turn);
    }
    return leftId - rightId;
  };

  const position: number[] = [];
  for (const list of outgoing) {
    list.sort(angleCompare);
    list.forEach((id, index) => {
      position[id] = index;
    });
  }

  return Object.freeze({
    vertices: Object.freeze(vertices),
    segments: Object.freeze(orderedSegments),
    halfedges: Object.freeze(halfedges),
    twin: Object.freeze(twin),
    outgoing: Object.freeze(outgoing.map((list) => Object.freeze(list))),
    position: Object.freeze(position),
  });
}

function insidePredicateForRegions(regions: readonly Region[]): (value: Point) => boolean {
  return (value: Point): boolean =>
    regions.every((region) =>
      region.polygons.some(
        (polygon) =>
          pointInRing(value, polygon.outer) &&
          !polygon.holes.some((hole) => pointInRing(value, hole)),
      ),
    );
}

export function feasibleComponentAreas(
  regionInputs: readonly RegionLike[],
  predicate?: (value: PointInput) => boolean,
): Rational[] {
  const regions = regionInputs.map((input) => normalizeRegion(input));
  const segments = regions.flatMap((region) => collectSegments(region));
  if (segments.length === 0) {
    return [];
  }
  const arrangement = buildArrangement(segments);
  const regionInside = insidePredicateForRegions(regions);
  const inside = (value: Point): boolean =>
    regionInside(value) &&
    (predicate === undefined ||
      predicate({ x: value.x, y: value.y }));
  return verticalDecompositionAreas(arrangement, inside);
}

interface SlabCell {
  readonly lowerLeft: Rational;
  readonly upperLeft: Rational;
  readonly lowerRight: Rational;
  readonly upperRight: Rational;
  readonly area: Rational;
}

class DisjointSet {
  readonly #parent: number[];
  readonly #size: number[];

  constructor(count: number) {
    this.#parent = Array.from({ length: count }, (_, index) => index);
    this.#size = Array.from({ length: count }, () => 1);
  }

  find(value: number): number {
    let current = value;
    while (this.#parent[current] !== current) {
      current = this.#parent[current]!;
    }
    let cursor = value;
    while (this.#parent[cursor] !== cursor) {
      const next = this.#parent[cursor]!;
      this.#parent[cursor] = current;
      cursor = next;
    }
    return current;
  }

  union(left: number, right: number): void {
    const first = this.find(left);
    const second = this.find(right);
    if (first === second) {
      return;
    }
    if (this.#size[first]! < this.#size[second]!) {
      this.#parent[first] = second;
      this.#size[second] = this.#size[second]! + this.#size[first]!;
    } else {
      this.#parent[second] = first;
      this.#size[first] = this.#size[first]! + this.#size[second]!;
    }
  }
}

function valueAtX(segment: Segment, x: Rational): Rational {
  return add(
    segment.a.y,
    mul(div(sub(x, segment.a.x), sub(segment.b.x, segment.a.x)), sub(segment.b.y, segment.a.y)),
  );
}

function verticalDecompositionAreas(
  arrangement: Arrangement,
  inside: (value: Point) => boolean,
): Rational[] {
  const xValues = new Map<string, Rational>();
  for (const vertex of arrangement.vertices) {
    xValues.set(rationalKey(vertex.x), vertex.x);
  }
  const orderedX = [...xValues.values()].sort((a, b) => cmp(a, b));
  const horizontalSegments = arrangement.segments.filter(
    (segment) => !eq(segment.a.x, segment.b.x),
  );
  const cells: SlabCell[] = [];
  const slabCells: number[][] = [];
  const slabGaps: number[][] = [];

  for (let slab = 0; slab < orderedX.length - 1; slab += 1) {
    const left = orderedX[slab]!;
    const right = orderedX[slab + 1]!;
    const middle = div(add(left, right), 2n);
    const width = sub(right, left);
    if (cmp(width, 0n) <= 0) {
      continue;
    }
    const active = horizontalSegments.filter(
      (segment) =>
        cmp(segment.a.x, segment.b.x) <= 0
          ? cmp(segment.a.x, left) <= 0 && cmp(segment.b.x, right) >= 0
          : cmp(segment.b.x, left) <= 0 && cmp(segment.a.x, right) >= 0,
    );
    if (active.length < 2) {
      slabCells.push([]);
      slabGaps.push([]);
      continue;
    }
    const atMiddle = new Map<Segment, Rational>();
    for (const segment of active) {
      atMiddle.set(segment, valueAtX(segment, middle));
    }
    active.sort((first, second) => {
      const difference = cmp(atMiddle.get(first)!, atMiddle.get(second)!);
      if (difference !== 0) {
        return difference;
      }
      return cmp(segmentSlope(first), segmentSlope(second));
    });
    const current: number[] = [];
    const gaps: number[] = [];
    for (let index = 0; index < active.length - 1; index += 1) {
      const lower = active[index]!;
      const upper = active[index + 1]!;
      const lowerLeft = valueAtX(lower, left);
      const upperLeft = valueAtX(upper, left);
      const lowerRight = valueAtX(lower, right);
      const upperRight = valueAtX(upper, right);
      const heightLeft = sub(upperLeft, lowerLeft);
      const heightRight = sub(upperRight, lowerRight);
      if (cmp(heightLeft, 0n) < 0 || cmp(heightRight, 0n) < 0) {
        continue;
      }
      const area = mul(width, div(add(heightLeft, heightRight), 2n));
      if (cmp(area, 0n) <= 0) {
        continue;
      }
      const middleHeight = sub(atMiddle.get(upper)!, atMiddle.get(lower)!);
      if (cmp(middleHeight, 0n) <= 0) {
        continue;
      }
      const sample = Object.freeze({
        x: middle,
        y: add(atMiddle.get(lower)!, div(middleHeight, 2n)),
      });
      if (!inside(sample)) {
        continue;
      }
      const cellIndex = cells.length;
      cells.push({
        lowerLeft,
        upperLeft,
        lowerRight,
        upperRight,
        area,
      });
      current.push(cellIndex);
      gaps.push(index);
    }
    slabCells.push(current);
    slabGaps.push(gaps);
  }

  if (cells.length === 0) {
    return [];
  }
  const sets = new DisjointSet(cells.length);
  for (let slab = 0; slab < slabCells.length; slab += 1) {
    const list = slabCells[slab]!;
    const gaps = slabGaps[slab]!;
    for (let index = 1; index < list.length; index += 1) {
      if (gaps[index] === gaps[index - 1]! + 1) {
        sets.union(list[index - 1]!, list[index]!);
      }
    }
  }

  for (let slab = 0; slab < slabCells.length - 1; slab += 1) {
    for (const leftIndex of slabCells[slab]!) {
      const left = cells[leftIndex]!;
      for (const rightIndex of slabCells[slab + 1]!) {
        const right = cells[rightIndex]!;
        const overlapLow =
          cmp(left.lowerRight, right.lowerLeft) >= 0 ? left.lowerRight : right.lowerLeft;
        const overlapHigh =
          cmp(left.upperRight, right.upperLeft) <= 0 ? left.upperRight : right.upperLeft;
        if (cmp(overlapHigh, overlapLow) > 0) {
          sets.union(leftIndex, rightIndex);
        }
      }
    }
  }

  const totals = new Map<number, Rational>();
  for (let index = 0; index < cells.length; index += 1) {
    const root = sets.find(index);
    totals.set(root, add(totals.get(root) ?? ZERO, cells[index]!.area));
  }
  const areas = [...totals.values()].filter((area) => cmp(area, 0n) > 0);
  areas.sort((a, b) => cmp(a, b));
  return areas;
}

function segmentSlope(segment: Segment): Rational {
  return div(sub(segment.b.y, segment.a.y), sub(segment.b.x, segment.a.x));
}

export function connectedComponentAreas(regionInput: RegionLike): Rational[] {
  return feasibleComponentAreas([regionInput]);
}

export const componentAreas = connectedComponentAreas;
export const intersectionComponentAreas = feasibleComponentAreas;

export function minimumComponentArea(regionInputs: readonly RegionLike[]): Rational {
  const areas = feasibleComponentAreas(regionInputs);
  return areas.length === 0 ? ZERO : areas[0]!;
}

export function regionArea(regionInput: RegionLike): Rational {
  return feasibleComponentAreas([regionInput]).reduce((total, area) => add(total, area), ZERO);
}

export function pointInPolygon(value: PointInput, polygonInput: PolygonLike): boolean {
  const polygons = normalizePolygon(polygonInput);
  const current = point(value);
  return polygons.some(
    (polygon) =>
      pointInRing(current, polygon.outer) &&
      !polygon.holes.some((hole) => pointInRing(current, hole)),
  );
}

export function polygonSignedArea(outer: RingInput, holes: readonly RingInput[] = []): Rational {
  const polygon = makePolygon(outer, holes);
  const outerArea = abs(ringSignedArea(polygon.outer));
  const holesArea = polygon.holes.reduce((total, hole) => add(total, abs(ringSignedArea(hole))), ZERO);
  return sub(outerArea, holesArea);
}
