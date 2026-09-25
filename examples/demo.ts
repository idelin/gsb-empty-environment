// Runnable sample: lays out the fixture grids and prints the results.
// Run with: npm run sample

import { layoutContainer } from "../src/layout.ts";
import { mixedGrid, crampedGrid, zeroWeightGrid, spanningGrid } from "../src/fixtures.ts";
import type { LayoutContainer } from "../src/tree.ts";

function show(name: string, container: LayoutContainer): void {
  const result = layoutContainer(container);
  console.log(`== ${name} (container ${container.width}px, gap ${container.gap}px)`);
  console.log(
    "  tracks:",
    result.trackSizes.map((size) => size.toFixed(2)).join(" | "),
    `(fraction iterations: ${result.fractionIterations})`,
  );
  for (const item of result.items) {
    console.log(
      `  item ${item.id.padEnd(6)} x=${item.x.toFixed(2).padStart(7)} ` +
        `width=${item.width.toFixed(2).padStart(7)} text=${item.textWidth.toFixed(2)}`,
    );
  }
  if (result.diagnostics.length === 0) {
    console.log("  diagnostics: none");
  } else {
    for (const diag of result.diagnostics) {
      console.log(`  diagnostics: [${diag.kind}] ${diag.message}`);
    }
  }
  console.log();
}

show("mixed grid", mixedGrid);
show("spanning grid", spanningGrid);
show("cramped grid (overflow)", crampedGrid);
show("zero-weight fractions", zeroWeightGrid);
