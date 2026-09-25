/**
 * Runnable sample: lays out the fixture tree and prints a report,
 * including an intentionally overflowing configuration to show diagnostics.
 */

import {
  FIXTURE_CONTAINER_WIDTH,
  FIXTURE_GAP,
  FIXTURE_TRACKS,
  FIXTURE_TREE,
} from "../src/fixtures.ts";
import { layout } from "../src/layout.ts";
import type { TrackSizingResult } from "../src/tracks.ts";

function printResult(title: string, result: TrackSizingResult): void {
  console.log(`\n=== ${title} ===`);
  console.log(
    `converged: ${result.converged} (iterations: ${result.iterations})`,
  );
  console.log(`used width: ${result.usedWidth.toFixed(1)}`);
  result.sizes.forEach((size, i) => {
    console.log(
      `  track ${i}: x=${result.positions[i].toFixed(1).padStart(7)}  size=${size.toFixed(1).padStart(7)}`,
    );
  });
  if (result.diagnostics.length === 0) {
    console.log("  diagnostics: none");
  } else {
    for (const d of result.diagnostics) {
      console.log(`  diagnostic [${d.kind}]: ${d.message}`);
    }
  }
}

const normal = layout({
  tree: FIXTURE_TREE,
  tracks: FIXTURE_TRACKS,
  containerWidth: FIXTURE_CONTAINER_WIDTH,
  gap: FIXTURE_GAP,
});
printResult(
  `fixture tree in ${FIXTURE_CONTAINER_WIDTH}px container (gap ${FIXTURE_GAP})`,
  normal,
);

const tight = layout({
  tree: FIXTURE_TREE,
  tracks: FIXTURE_TRACKS,
  containerWidth: 200,
  gap: FIXTURE_GAP,
});
printResult("same tree in a 200px container (overflow expected)", tight);

const zeroWeight = layout({
  tree: FIXTURE_TREE,
  tracks: [
    { kind: "fixed", size: 100 },
    { kind: "proportional", weight: 0 },
    { kind: "proportional", weight: 0 },
  ],
  containerWidth: 500,
  gap: 10,
});
printResult("all-zero proportional weights (no division by zero)", zeroWeight);
