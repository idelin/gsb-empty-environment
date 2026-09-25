import { writeFileSync, mkdirSync } from "node:fs";
import {
  renderScene,
  group,
  shapeNode,
  invertNode,
  type SceneNode,
} from "../src/scene.ts";
import { referenceCoverage } from "../src/reference.ts";
import { rectShape, pathShape } from "../src/geometry.ts";
import { translate, rotate, scale, multiply } from "../src/matrix.ts";
import {
  RGBAImage,
  paint,
  applyMask,
  maskToAscii,
  maskToPgm,
  imageToPpm,
} from "../src/image.ts";
import { Mask, subtract } from "../src/mask.ts";

const W = 32;
const H = 22;

// Scene: rects + paths + inverse mask, boolean union/intersect/subtract,
// nested transforms (group transform * child transform).
const scene: SceneNode = group("union", [
  // 1) rotated rect minus a diamond (boolean subtract, nested transform)
  group(
    "subtract",
    [
      shapeNode(rectShape(-5, -4, 10, 8)),
      shapeNode(pathShape([[0, -3, 3, 0, 0, 3, -3, 0]])),
    ],
    multiply(translate(8, 8), rotate(0.25)),
  ),
  // 2) intersection of a triangle path and a sheared rect
  group("intersect", [
    shapeNode(pathShape([[14, 2, 24, 4, 18, 12]])),
    shapeNode(rectShape(15, 3, 8, 6), rotate(-0.12)),
  ]),
  // 3) inverse mask: complement of a small rect, restricted to a sub-region
  invertNode(shapeNode(rectShape(26, 13, 4, 4)), { x: 24, y: 11, w: 8, h: 8 }),
  // 4) deeply nested: translate * rotate * scale around a local origin
  group(
    "union",
    [
      shapeNode(rectShape(-2, -1.5, 4, 3)),
      shapeNode(pathShape([[0, -3, 2.5, 2.5, -2.5, 2.5]]), translate(3, 0)),
    ],
    multiply(translate(9, 17), multiply(rotate(-0.35), scale(1.1, 0.9))),
  ),
]);

console.log(`canvas ${W}x${H}, fast ss=8, reference ss=32\n`);

const fast = renderScene(scene, W, H, 8);
const ref = referenceCoverage(scene, W, H, 32);

// Per-pixel comparison against the brute-force reference.
let maxDiff = 0;
let mismatches = 0;
let outOfRange = 0;
for (let i = 0; i < fast.data.length; i++) {
  const f = fast.data[i];
  if (f < 0 || f > 1) outOfRange++;
  const d = Math.abs(f - ref.data[i]);
  if (d > maxDiff) maxDiff = d;
  if (d > 0.15) mismatches++;
}
console.log(`per-pixel vs reference: max |diff| = ${maxDiff.toFixed(4)}`);
console.log(`pixels beyond tolerance 0.15: ${mismatches}`);
console.log(`coverage values outside [0,1]: ${outOfRange}`);

// Repeated mask application follows the math definition (multiplies each time).
const img = RGBAImage.filled(W, H, [255, 255, 255, 255]);
paint(img, fast, [32, 64, 192, 255]);
const hole = subtract(
  Mask.filled(W, H, 1),
  renderScene(shapeNode(rectShape(14, 14, 5, 4)), W, H, 8),
);
applyMask(img, hole); // punch a soft-edged hole into the alpha channel

console.log("\ncoverage mask (ascii):");
console.log(maskToAscii(fast));

mkdirSync("artifacts", { recursive: true });
writeFileSync("artifacts/demo-mask.pgm", maskToPgm(fast));
writeFileSync("artifacts/demo.ppm", imageToPpm(img));
console.log("\nwrote artifacts/demo-mask.pgm and artifacts/demo.ppm");

if (mismatches > 0 || outOfRange > 0) {
  console.error("\nDEMO FAILED: fast path diverges from reference");
  process.exit(1);
}
console.log("\nDEMO OK: fast path matches per-pixel reference");
