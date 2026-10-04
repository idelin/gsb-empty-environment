import { readFileSync, mkdirSync, writeFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";
import { stripTypes } from "../scripts/strip-types.mjs";

const here = dirname(fileURLToPath(import.meta.url));
const src = readFileSync(join(here, "../src/seam-carver.ts"), "utf8");
mkdirSync(join(here, "../test/.build"), { recursive: true });
const outFile = join(here, "../test/.build/seam-carver.mjs");
writeFileSync(outFile, stripTypes(src));
const { SeamCarver, gradientEnergy } = await import(outFile);

// 8x6 grayscale image with a vertical edge and a bright protected spot.
const gray = [
  [10, 10, 10, 200, 200, 200, 200, 200],
  [10, 10, 10, 200, 200, 200, 200, 200],
  [10, 10, 10, 200, 255, 200, 200, 200],
  [10, 10, 10, 200, 200, 200, 200, 200],
  [10, 10, 10, 200, 200, 200, 200, 200],
  [10, 10, 10, 200, 200, 200, 200, 200],
];

const carver = new SeamCarver(gradientEnergy(gray), {
  protectedPoints: [{ x: 4, y: 2 }],
  // Recompute gradient energy on the remaining pixels after every removal.
  recomputeEnergy: (remaining) =>
    remaining.map((row, y) =>
      row.map((p, x) => {
        const left = row[Math.max(0, x - 1)];
        const right = row[Math.min(row.length - 1, x + 1)];
        const upRow = remaining[Math.max(0, y - 1)];
        const downRow = remaining[Math.min(remaining.length - 1, y + 1)];
        const up = upRow[Math.min(x, upRow.length - 1)];
        const down = downRow[Math.min(x, downRow.length - 1)];
        const gx = Math.abs(gray[right.y][right.x] - gray[left.y][left.x]);
        const gy = Math.abs(gray[down.y][down.x] - gray[up.y][up.x]);
        return gx + gy;
      })
    ),
});

const report = carver.carve(4);
for (const seam of report.seams) {
  const cols = seam.path.map((p) => p.x).join(",");
  console.log("round " + seam.round + ": energy=" + seam.energy + "  path(x per row)=" + cols);
}
if (!report.feasible) {
  console.log("stopped early: " + report.reason);
}
console.log("removed " + report.removed + " seam(s), remaining width " + carver.width);
