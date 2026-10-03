import { readFileSync } from "node:fs";
import { point } from "./geometry.ts";
import type { Point } from "./geometry.ts";
import { verifySeparability } from "./separate.ts";
import type { SeparationResult } from "./separate.ts";

type RawCoord = number | string;
interface RawInput {
  A: [RawCoord, RawCoord][];
  B: [RawCoord, RawCoord][];
}

function parseSet(raw: [RawCoord, RawCoord][], name: string): Point[] {
  if (!Array.isArray(raw) || raw.length === 0) {
    throw new Error(`set ${name} must be a non-empty array of [x, y] pairs`);
  }
  return raw.map(([x, y]) => point(x, y));
}

function render(result: SeparationResult): string {
  const lines: string[] = [];
  if (result.separable) {
    lines.push("verdict: SEPARABLE");
    lines.push(`hyperplane: ${result.hyperplane.equation}`);
    lines.push(
      `coefficients: a=${result.hyperplane.a}, b=${result.hyperplane.b}, c=${result.hyperplane.c}`,
    );
    lines.push(`closest point in conv(A): (${result.witnessA.x}, ${result.witnessA.y})`);
    lines.push(`closest point in conv(B): (${result.witnessB.x}, ${result.witnessB.y})`);
    lines.push(`strict margins: min_A h = ${result.minSideA} > 0, max_B h = ${result.maxSideB} < 0`);
  } else {
    lines.push("verdict: NOT SEPARABLE (convex hulls intersect)");
    lines.push(
      `overlap region vertices: ${result.overlap.map((p) => `(${p.x}, ${p.y})`).join(" ")}`,
    );
    lines.push(`common point: (${result.point.x}, ${result.point.y})`);
    const fmt = (cert: { points: Point[]; weights: import("./rational.ts").Rational[] }) =>
      cert.points.map((p, i) => `${cert.weights[i]}*(${p.x}, ${p.y})`).join(" + ");
    lines.push(`in conv(A): ${fmt(result.certificateA)}`);
    lines.push(`in conv(B): ${fmt(result.certificateB)}`);
  }
  return lines.join("\n");
}

function main(): void {
  const arg = process.argv[2];
  const rawText = arg && arg !== "-" ? readFileSync(arg, "utf8") : readFileSync(0, "utf8");
  const input = JSON.parse(rawText) as RawInput;
  const A = parseSet(input.A, "A");
  const B = parseSet(input.B, "B");
  const result = verifySeparability(A, B);
  console.log(render(result));
  console.log("\nJSON:");
  console.log(JSON.stringify(result, null, 2));
}

main();
