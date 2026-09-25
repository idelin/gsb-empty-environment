import type { MergeRegion } from "../types.ts";

/**
 * Node-tree fixture.
 *
 * Mimics the DOM-like tree a browser would hand to its layout engine:
 * a grid node with row nodes, each holding cell nodes with text runs.
 * The layout module consumes this tree plus the glyph fixture instead
 * of a browser layout engine.
 */
export interface CellNode {
  kind: "cell";
  text: string;
}

export interface RowNode {
  kind: "row";
  children: CellNode[];
}

export interface GridNode {
  kind: "grid";
  merges: MergeRegion[];
  children: RowNode[];
}

export function cell(text: string): CellNode {
  return { kind: "cell", text };
}

export function row(...texts: string[]): RowNode {
  return { kind: "row", children: texts.map(cell) };
}

/**
 * Sample document used by the demo and the tests.
 *
 *   A1: "Quarter"   B1: "Revenue"  C1: "Revenue"  D1: "Delta"
 *   A2: "Q1"        B2: "120.5"    C2: covered    D2: "4.2%"
 *   A3: "Q2"        B3: covered    C3: covered    D3: "-1.3%"
 *   A4: "Total"     B4: covered    C4: covered    D4: "3.1%"
 *
 * Merges: B1:C1 (header), B2:C3 (chain-adjacent to header via column B),
 * A4:C4 (footer band). A4:C4 touches B2:C3 at B3/B4? No - it starts at
 * row 4, so a selection of B3:B4 must expand through both regions.
 */
export const fixtureGrid: GridNode = {
  kind: "grid",
  merges: [
    { anchor: { row: 0, col: 1 }, rows: 1, cols: 2 }, // B1:C1
    { anchor: { row: 1, col: 1 }, rows: 2, cols: 2 }, // B2:C3
    { anchor: { row: 3, col: 0 }, rows: 1, cols: 3 }, // A4:C4
  ],
  children: [
    row("Quarter", "Revenue", "", "Delta"),
    row("Q1", "120.5", "", "4.2%"),
    row("Q2", "", "", "-1.3%"),
    row("Total", "", "", "3.1%"),
  ],
};
