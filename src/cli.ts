#!/usr/bin/env node
/**
 * CLI for the segmented regression solver.
 *
 * Reads a JSON document from stdin (or from the file named by argv[2]):
 *   { "x": [0, 1, 2, ...], "y": [3, 5, 7, ...], "maxSegments": 3,
 *     "minSegmentLength": 3 }                      // optional, defaults to 3
 * or
 *   { "points": [[0, 3], [1, 5], ...], "maxSegments": 3 }
 *
 * Prints the optimal segmentation as JSON. Slopes, intercepts and errors are
 * exact rationals rendered as "p/q" strings.
 */
import { readFileSync } from "node:fs";
import { fitSegmentedRegression } from "./segmentedRegression.ts";

interface CliInput {
  x?: (number | bigint)[];
  y?: (number | bigint)[];
  points?: [number, number][];
  maxSegments?: number;
  minSegmentLength?: number;
}

function main(): void {
  const raw = process.argv[2]
    ? readFileSync(process.argv[2], "utf8")
    : readFileSync(0, "utf8");
  const input = JSON.parse(raw) as CliInput;

  let xs: (number | bigint)[];
  let ys: (number | bigint)[];
  if (input.points) {
    xs = input.points.map((p) => p[0]);
    ys = input.points.map((p) => p[1]);
  } else if (input.x && input.y) {
    xs = input.x;
    ys = input.y;
  } else {
    throw new Error('input must contain "x" and "y" arrays, or "points"');
  }
  const maxSegments = input.maxSegments ?? 3;
  const minSegmentLength = input.minSegmentLength ?? 3;

  const result = fitSegmentedRegression(xs, ys, maxSegments, minSegmentLength);

  const output = {
    segments: result.segments.map((s) => ({
      start: s.start,
      end: s.end,
      slope: s.slope.toString(),
      intercept: s.intercept.toString(),
      sse: s.sse.toString(),
      degenerate: s.degenerate,
    })),
    breakpoints: result.breakpoints,
    totalSSE: result.totalSSE.toString(),
  };
  process.stdout.write(JSON.stringify(output, null, 2) + "\n");
}

try {
  main();
} catch (err) {
  process.stderr.write(`error: ${(err as Error).message}\n`);
  process.exit(1);
}
