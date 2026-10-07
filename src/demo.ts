import {
  computeConflictPairs,
  fourCandidates,
  solveLabelLayout,
  Label,
} from "./solver";
import { verifyLayout } from "./verify";

const canvas = { x: 0, y: 0, w: 120, h: 90 };

// Twelve labels on a coarse grid; tight spacing forces real conflicts and
// the canvas edges cut off some candidates.
const labels: Label[] = [];
const weights = [9, 4, 7, 2, 8, 5, 6, 3, 10, 1, 5, 7];
let k = 0;
for (const ax of [15, 45, 75, 105]) {
  for (const ay of [20, 50, 80]) {
    labels.push({
      id: `L${k}`,
      anchor: { x: ax, y: ay },
      weight: weights[k],
      candidates: fourCandidates({ x: ax, y: ay }, 22, 10, 1),
    });
    k++;
  }
}

const result = solveLabelLayout({ canvas, labels });

console.log(`Canvas ${canvas.w}x${canvas.h}, ${labels.length} labels, 4 candidates each\n`);
console.log("Placed:");
for (const p of result.placed) {
  console.log(
    `  ${p.id}  candidate=${p.candidateIndex}  weight=${p.weight}  ` +
      `displacement=${p.displacement.toFixed(3)}  ` +
      `rect=(${p.rect.x},${p.rect.y},${p.rect.w}x${p.rect.h})`,
  );
}
console.log(`Hidden (unplaced): ${result.unplaced.join(", ") || "none"}`);
console.log(
  `Total weight=${result.totalWeight}  total displacement=${result.totalDisplacement.toFixed(3)}\n`,
);

const conflicts = computeConflictPairs(labels);
console.log(`Conflict pairs (overlapping candidate combos): ${conflicts.length}`);
const byPair = new Map<string, number>();
for (const c of conflicts) {
  const key = `${c.a}~${c.b}`;
  byPair.set(key, (byPair.get(key) ?? 0) + 1);
}
for (const [pair, count] of [...byPair.entries()].sort()) {
  console.log(`  ${pair}: ${count} candidate combination(s) overlap`);
}

const report = verifyLayout({ canvas, labels }, result);
console.log(
  `\nExhaustive verification: ${report.ok ? "OK" : "FAILED"} ` +
    `(${report.assignmentsExplored} feasible assignments enumerated; ` +
    `optimum weight=${report.optimalWeight}, displacement=${report.optimalDisplacement.toFixed(3)})`,
);
for (const err of report.errors) console.log(`  ERROR: ${err}`);
if (!report.ok) process.exit(1);
