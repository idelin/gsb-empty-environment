import assert from "node:assert/strict";
import { test } from "node:test";
import {
  DragCanceledError,
  TrajectoryRemapError,
  createDragSession,
  createRecordingInjector,
  executeDrag,
  executeDragSync,
  planPath,
  pointLocation,
  remapTrajectory,
  segmentBlockedByObstacles,
} from "../index.ts";
import type { Point, Polygon } from "../index.ts";

function rectangle(left: number, top: number, right: number, bottom: number): Point[] {
  return [
    { x: left, y: top },
    { x: right, y: top },
    { x: right, y: bottom },
    { x: left, y: bottom },
  ];
}

function near(actual: number, expected: number, epsilon = 1e-9): void {
  assert.ok(Math.abs(actual - expected) <= epsilon, `expected ${actual} to be near ${expected}`);
}

function pathLength(path: Point[]): number {
  let total = 0;
  for (let index = 1; index < path.length; index += 1) {
    total += Math.hypot(path[index].x - path[index - 1].x, path[index].y - path[index - 1].y);
  }
  return total;
}

function bends(path: Point[]): number {
  let count = 0;
  for (let index = 1; index + 1 < path.length; index += 1) {
    const ax = path[index].x - path[index - 1].x;
    const ay = path[index].y - path[index - 1].y;
    const bx = path[index + 1].x - path[index].x;
    const by = path[index + 1].y - path[index].y;
    const cross = ax * by - ay * bx;
    if (Math.abs(cross) > 1e-9) {
      count += 1;
    }
  }
  return count;
}

/**
 * Small independent visibility-graph reference: sample every candidate
 * segment, enumerate simple paths, then sort by (bends, length, vertex ids).
 * This is intentionally separate from the production graph implementation.
 */
function referencePath(start: Point, goal: Point, obstacles: Polygon[]) {
  const vertices = [start, goal];
  const ids: Array<string | number> = ["start", "goal"];
  let nextId = 0;
  for (const obstacle of obstacles) {
    for (const vertex of obstacle.vertices) {
      vertices.push({ x: vertex.x, y: vertex.y });
      ids.push(vertex.id ?? nextId);
      nextId += 1;
    }
  }

  const isPointInside = (point: Point, polygon: Polygon): boolean => {
    const pointOnBoundary = polygon.vertices.some((a, index) => {
      const b = polygon.vertices[(index + 1) % polygon.vertices.length];
      const abx = b.x - a.x;
      const aby = b.y - a.y;
      const apx = point.x - a.x;
      const apy = point.y - a.y;
      const cross = Math.abs(abx * apy - aby * apx);
      const along = apx * abx + apy * aby;
      const lengthSquared = abx * abx + aby * aby;
      return cross <= 1e-9 && along >= -1e-9 && along <= lengthSquared + 1e-9;
    });
    if (pointOnBoundary) {
      return false;
    }

    let inside = false;
    for (let index = 0; index < polygon.vertices.length; index += 1) {
      const a = polygon.vertices[index];
      const b = polygon.vertices[(index + 1) % polygon.vertices.length];
      const straddles = (a.y > point.y) !== (b.y > point.y);
      if (straddles) {
        const x = a.x + ((point.y - a.y) * (b.x - a.x)) / (b.y - a.y);
        if (point.x < x) {
          inside = !inside;
        }
      }
    }
    return inside;
  };

  const visible = (a: Point, b: Point): boolean => {
    for (const obstacle of obstacles) {
      for (let sample = 1; sample < 200; sample += 1) {
        const t = sample / 200;
        const point = { x: a.x + (b.x - a.x) * t, y: a.y + (b.y - a.y) * t };
        if (isPointInside(point, obstacle)) {
          return false;
        }
      }
    }
    return true;
  };

  const adjacency: number[][] = vertices.map(() => []);
  for (let from = 0; from < vertices.length; from += 1) {
    for (let to = from + 1; to < vertices.length; to += 1) {
      if (visible(vertices[from], vertices[to])) {
        adjacency[from].push(to);
        adjacency[to].push(from);
      }
    }
  }

  let best: { path: Point[]; ids: Array<string | number>; bends: number; length: number } | null = null;
  const visit = (current: number, path: number[]): void => {
    if (path.length > 10) {
      return;
    }
    if (current === 1) {
      const points = path.map((index) => vertices[index]);
      const candidateBends = bends(points);
      const candidateLength = pathLength(points);
      const candidateIds = path.map((index) => ids[index]);
      if (!best) {
        best = { path: points, ids: candidateIds, bends: candidateBends, length: candidateLength };
        return;
      }
      if (candidateBends < best.bends) {
        best = { path: points, ids: candidateIds, bends: candidateBends, length: candidateLength };
        return;
      }
      if (candidateBends !== best.bends) {
        return;
      }
      if (candidateLength + 1e-9 < best.length) {
        best = { path: points, ids: candidateIds, bends: candidateBends, length: candidateLength };
        return;
      }
      if (Math.abs(candidateLength - best.length) <= 1e-9) {
        const left = candidateIds.map(String);
        const right = best.ids.map(String);
        const comparison = left.join("|") < right.join("|") ? -1 : left.join("|") > right.join("|") ? 1 : 0;
        if (comparison < 0) {
          best = { path: points, ids: candidateIds, bends: candidateBends, length: candidateLength };
        }
      }
      return;
    }

    for (const next of adjacency[current]) {
      if (path.includes(next)) {
        continue;
      }
      path.push(next);
      visit(next, path);
      path.pop();
    }
  };

  visit(0, [0]);
  assert.ok(best);
  return best!;
}

test("remaps recorded points through rotation, DPI scale and translation before planning", () => {
  const transform = [0, 2, -2, 0, 100, 50];
  const result = remapTrajectory({
    trajectory: [[0, 0], [5, 0], [10, 0], [10, 10]],
    transform,
    region: { x: -20, y: -20, width: 300, height: 300 },
    obstacles: [],
  });

  assert.deepEqual(result.trajectory, [
    { x: 100, y: 50 },
    { x: 100, y: 60 },
    { x: 100, y: 70 },
    { x: 80, y: 70 },
  ]);
  assert.deepEqual(result.start, { x: 100, y: 50 });
  assert.deepEqual(result.end, { x: 80, y: 70 });
  near(result.length, Math.hypot(20, 20));
  assert.equal(result.bends, 0);
});

test("accepts convenience scale/translate transform parts", () => {
  const result = remapTrajectory(
    [[1, 2], [3, 4]],
    { scale: [2, 2], translate: [10, 20] },
    { minX: 0, minY: 0, maxX: 100, maxY: 100 },
    [],
  );
  assert.deepEqual(result.trajectory, [{ x: 12, y: 24 }, { x: 16, y: 28 }]);
});

test("composes recorded and current window transforms as current * inverse(recorded)", () => {
  const result = remapTrajectory({
    recordedTrajectory: [[10, 0], [20, 0]],
    recordedTransform: { a: 2, b: 0, c: 0, d: 2, e: 0, f: 0 },
    currentTransform: { a: 4, b: 0, c: 0, d: 4, e: 10, f: 10 },
    region: { x: 0, y: 0, width: 200, height: 200 },
    obstacles: [],
  });
  assert.deepEqual(result.trajectory, [{ x: 30, y: 10 }, { x: 50, y: 10 }]);
  near(result.transform.a, 2);
  near(result.transform.e, 10);
});

test("chooses fewest bends, then shortest length, around a rectangle", () => {
  const start = { x: -2, y: 1 };
  const goal = { x: 2, y: 1 };
  const obstacles = [rectangle(0, 0, 1, 2)];
  const result = planPath({ start, goal, obstacles });

  assert.equal(result.bends, 2);
  assert.equal(result.segments, 3);
  near(result.length, 4.650281539872885);
  assert.deepEqual(result.vertexIds, ["start", 0, 1, "goal"]);
});

test("uses the smallest vertex ids to break a symmetric tie", () => {
  const start = { x: -2, y: 1 };
  const goal = { x: 2, y: 1 };
  const obstacles = [rectangle(0, 0, 1, 2)];
  const result = planPath({ start, goal, obstacles });

  assert.deepEqual(result.path, [
    { x: -2, y: 1 },
    { x: 0, y: 0 },
    { x: 1, y: 0 },
    { x: 2, y: 1 },
  ]);
});

test("allows edge-hugging and vertex contact but rejects crossing the interior", () => {
  const obstacles = [rectangle(0, 0, 2, 1)];
  const start = { x: -1, y: 0 };
  const goal = { x: 3, y: 0 };

  assert.equal(segmentBlockedByObstacles(start, goal, [{ vertices: obstacles[0].map((p) => ({ ...p })) }]), false);
  const edgeHugging = planPath({ start, goal, obstacles });
  assert.equal(edgeHugging.bends, 0);
  assert.equal(edgeHugging.segments, 1);
  near(edgeHugging.length, 4);

  const crossing = planPath({
    start: { x: -1, y: 0.5 },
    goal: { x: 3, y: 0.5 },
    obstacles,
  });
  assert.ok(crossing.bends >= 2);
  assert.ok(crossing.length > 4);
});

test("matches the small visibility-graph reference under rotation", () => {
  const rotation = Math.PI / 7;
  const matrix = [
    Math.cos(rotation) * 1.5,
    Math.sin(rotation) * 1.5,
    -Math.sin(rotation) * 1.5,
    Math.cos(rotation) * 1.5,
    20,
    -7,
  ];
  const apply = (point: Point): Point => ({
    x: matrix[0] * point.x + matrix[2] * point.y + matrix[4],
    y: matrix[1] * point.x + matrix[3] * point.y + matrix[5],
  });

  const start = apply({ x: -2, y: 1 });
  const goal = apply({ x: 3, y: 1 });
  const obstacle: Polygon = {
    vertices: [
      { x: 0, y: 0, id: 0 },
      { x: 2, y: 0, id: 1 },
      { x: 2, y: 2, id: 2 },
      { x: 0, y: 2, id: 3 },
    ].map((point) => ({ ...apply(point), id: point.id })),
  };

  const result = planPath({ start, goal, obstacles: [obstacle] });
  const reference = referencePath(start, goal, [obstacle]);

  assert.equal(result.bends, reference.bends);
  near(result.length, reference.length, 1e-7);
  assert.deepEqual(
    result.path.map((point) => [Math.round(point.x * 8) / 8, Math.round(point.y * 8) / 8]),
    reference.path.map((point) => [Math.round(point.x * 8) / 8, Math.round(point.y * 8) / 8]),
  );
});

test("rejects start/end outside the target region", () => {
  assert.throws(
    () =>
      remapTrajectory({
        trajectory: [[1, 1], [0, 0]],
        transform: { a: 10, b: 0, c: 0, d: 10, e: 0, f: 0 },
        region: { x: 0, y: 0, width: 5, height: 5 },
        obstacles: [],
      }),
    (error: unknown) => error instanceof TrajectoryRemapError && error.code === "START_OUTSIDE_REGION",
  );

  assert.throws(
    () =>
      remapTrajectory({
        trajectory: [[0, 0], [1, 1]],
        transform: { a: 10, b: 0, c: 0, d: 10, e: 0, f: 0 },
        region: { x: -100, y: -100, width: 200, height: 100 },
        obstacles: [],
      }),
    (error: unknown) => error instanceof TrajectoryRemapError && error.code === "END_OUTSIDE_REGION",
  );
});

test("rejects a mapped endpoint strictly inside an obstacle but permits its boundary", () => {
  const obstacle = rectangle(0, 0, 10, 10);
  assert.throws(
    () =>
      remapTrajectory({
        trajectory: [[0, 0], [5, 5]],
        transform: { a: 1, b: 0, c: 0, d: 1, e: 0, f: 0 },
        region: { x: -20, y: -20, width: 100, height: 100 },
        obstacles: [obstacle],
      }),
    (error: unknown) => error instanceof TrajectoryRemapError && error.code === "END_INSIDE_OBSTACLE",
  );

  const boundary = remapTrajectory({
    trajectory: [[-1, 0], [5, 0]],
    transform: { a: 1, b: 0, c: 0, d: 1, e: 0, f: 0 },
    region: { x: -20, y: -20, width: 100, height: 100 },
    obstacles: [obstacle],
  });
  assert.equal(boundary.bends, 0);
});

test("executes exactly one mouse-down and one mouse-up", async () => {
  const recorder = createRecordingInjector();
  const result = await executeDrag(
    [
      { x: 0, y: 1 },
      { x: 1, y: 0 },
      { x: 3, y: 1 },
    ],
    recorder,
  );

  const types = recorder.events.map((event) => event.type);
  assert.deepEqual(types, ["move", "mousedown", "move", "move", "mouseup"]);
  assert.equal(result.mousedownCount, 1);
  assert.equal(result.mouseupCount, 1);
  assert.equal(types.filter((type) => type === "mousedown").length, 1);
  assert.equal(types.filter((type) => type === "mouseup").length, 1);
});

test("releases once when a drag is canceled after mouse-down", async () => {
  const recorder = createRecordingInjector();
  const controller = new AbortController();
  const injector = {
    moveTo(point: Point) {
      recorder.moveTo(point);
      if (point.x === 1) {
        controller.abort();
      }
    },
    mouseDown(point: Point) {
      recorder.mouseDown(point);
    },
    mouseUp(point: Point) {
      recorder.mouseUp(point);
    },
  };

  await assert.rejects(
    executeDrag(
      [
        { x: 0, y: 0 },
        { x: 1, y: 0 },
        { x: 2, y: 0 },
      ],
      injector,
      { signal: controller.signal },
    ),
    DragCanceledError,
  );

  const types = recorder.events.map((event) => event.type);
  assert.equal(types.filter((type) => type === "mousedown").length, 1);
  assert.equal(types.filter((type) => type === "mouseup").length, 1);
  assert.equal(types[types.length - 1], "mouseup");
});

test("can resolve a canceled drag after releasing when throwOnCancel is false", async () => {
  const recorder = createRecordingInjector();
  const controller = new AbortController();
  const injector = {
    moveTo(point: Point) {
      recorder.moveTo(point);
      if (point.x === 1) {
        controller.abort();
      }
    },
    mouseDown(point: Point) {
      recorder.mouseDown(point);
    },
    mouseUp(point: Point) {
      recorder.mouseUp(point);
    },
  };

  const result = await executeDrag(
    [
      { x: 0, y: 0 },
      { x: 1, y: 0 },
      { x: 2, y: 0 },
    ],
    injector,
    { signal: controller.signal, throwOnCancel: false },
  );
  assert.equal(result.canceled, true);
  assert.equal(result.mousedownCount, 1);
  assert.equal(result.mouseupCount, 1);
});

test("a drag session canceled before injection releases nothing but never presses", async () => {
  const recorder = createRecordingInjector();
  const session = createDragSession(
    [
      { x: 0, y: 0 },
      { x: 1, y: 0 },
    ],
    recorder,
  );
  session.cancel();
  await assert.rejects(session.promise, DragCanceledError);
  assert.equal(recorder.events.length, 0);
});

test("releases after an injector error and supports x/y injector methods", () => {
  const calls: string[] = [];
  const injector = {
    moveTo(x: number, y: number) {
      calls.push(`move:${x},${y}`);
      if (x === 2) {
        throw new Error("injection failed");
      }
    },
    mouseDown() {
      calls.push("down");
    },
    mouseUp() {
      calls.push("up");
    },
  };

  assert.throws(
    () =>
      executeDragSync(
        [
          { x: 0, y: 0 },
          { x: 1, y: 0 },
          { x: 2, y: 0 },
        ],
        injector,
      ),
    /injection failed/,
  );
  assert.deepEqual(calls.filter((call) => call === "down"), ["down"]);
  assert.deepEqual(calls.filter((call) => call === "up"), ["up"]);
});

test("visibility classification treats boundary contact as legal", () => {
  const polygon: Polygon = { vertices: rectangle(0, 0, 1, 1) };
  assert.equal(pointLocation(polygon, { x: 0, y: 0.5 }), "boundary");
  assert.equal(pointLocation(polygon, { x: 0.5, y: 0.5 }), "inside");
  assert.equal(pointLocation(polygon, { x: 2, y: 2 }), "outside");
});
