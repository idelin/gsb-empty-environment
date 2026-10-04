import { readFileSync } from "fs";
import {
  fitSegmentedRegression,
  IntInput,
  SingularMode,
} from "./segmented-regression";

interface CliInput {
  x?: IntInput[];
  y?: IntInput[];
  points?: [IntInput, IntInput][];
  maxSegments?: number;
  minSegmentLength?: number;
  singular?: SingularMode;
}

function serialize(value: unknown): unknown {
  if (typeof value === "bigint") return value.toString();
  if (Array.isArray(value)) return value.map(serialize);
  if (value !== null && typeof value === "object") {
    const out: Record<string, unknown> = {};
    for (const [k, v] of Object.entries(value)) out[k] = serialize(v);
    return out;
  }
  return value;
}

function main(): void {
  const raw = readFileSync(0, "utf8");
  const input = JSON.parse(raw) as CliInput;

  let xs: IntInput[];
  let ys: IntInput[];
  if (input.points) {
    xs = input.points.map((p) => p[0]);
    ys = input.points.map((p) => p[1]);
  } else if (input.x && input.y) {
    xs = input.x;
    ys = input.y;
  } else {
    throw new TypeError('Input JSON must contain "points" or both "x" and "y"');
  }

  const result = fitSegmentedRegression(xs, ys, {
    maxSegments: input.maxSegments ?? 3,
    minSegmentLength: input.minSegmentLength ?? 3,
    singular: input.singular ?? "constant",
  });

  const output = {
    segmentCount: result.segmentCount,
    breakpoints: result.breakpoints,
    totalSse: result.totalSse.toString(),
    totalSseApprox: result.totalSse.toNumber(),
    segments: result.segments.map((seg) => ({
      start: seg.start,
      end: seg.end,
      count: seg.count,
      slope: seg.slope.toString(),
      intercept: seg.intercept.toString(),
      slopeApprox: seg.slope.toNumber(),
      interceptApprox: seg.intercept.toNumber(),
      sse: seg.sse.toString(),
      singular: seg.singular,
    })),
  };
  process.stdout.write(JSON.stringify(serialize(output), null, 2) + "\n");
}

try {
  main();
} catch (err) {
  const message = err instanceof Error ? `${err.name}: ${err.message}` : String(err);
  process.stderr.write(message + "\n");
  process.exit(1);
}
