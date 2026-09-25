import { Grid } from "../src/grid.ts";
import { fixtureGrid } from "../src/fixtures/nodeTree.ts";
import type { Direction, Pos } from "../src/index.ts";

const grid = new Grid(fixtureGrid);

const colName = (col: number) => String.fromCharCode(65 + col);
const name = (pos: Pos) => `${colName(pos.col)}${pos.row + 1}`;

console.log("== Layout (fixture node tree + glyph metrics, no browser) ==");
console.log(`rows=${grid.rows} cols=${grid.cols}`);
console.log(`columnOffsets=[${grid.geometry.columnOffsets.join(", ")}]`);
console.log(`rowOffsets=[${grid.geometry.rowOffsets.join(", ")}]`);
const hit = grid.cellAtPoint(30, 5);
console.log(`hitTest(30, 5) -> ${hit ? name(hit) : "null"}`);

console.log("\n== Navigation (jumps never land on covered cells) ==");
let active: Pos = { row: 0, col: 0 }; // A1
console.log(`start at ${name(active)}`);
const keys: Direction[] = ["right", "right", "down", "down", "left", "left", "up"];
for (const key of keys) {
  active = grid.move(active, key);
  console.log(`Arrow ${key.padEnd(5)} -> ${name(active)}`);
}

console.log("\n== Selection (touching a merge covers the whole region) ==");
const sel1 = grid.select({ anchor: { row: 0, col: 0 }, focus: { row: 1, col: 1 } });
console.log("drag A1..B2 ->", JSON.stringify(sel1), "(B1:C1 and B2:C3 pulled in)");

console.log("\n== Chained merges (expansion runs to a fixed point) ==");
const sel2 = grid.select({ anchor: { row: 2, col: 1 }, focus: { row: 3, col: 1 } });
console.log("drag B3..B4 ->", JSON.stringify(sel2), "(B2:C3 then A4:C4)");

console.log("\n== Copy (rectangular, empty placeholders for covered cells) ==");
const matrix = grid.copyMatrix({ anchor: { row: 0, col: 0 }, focus: { row: 3, col: 3 } });
for (const line of matrix) console.log(JSON.stringify(line));
console.log("\n-- TSV --");
console.log(grid.copyTSV({ anchor: { row: 0, col: 0 }, focus: { row: 3, col: 3} }));
