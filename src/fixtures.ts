/**
 * Fixture data: the node tree and the grid configuration used by the demo
 * and the tests. Everything is static and deterministic.
 */

import type { LayoutNode } from "./tree.ts";
import type { TrackDefinition } from "./tracks.ts";

export const FIXTURE_TRACKS: readonly TrackDefinition[] = [
  { kind: "fixed", size: 120 },
  { kind: "content" },
  { kind: "proportional", weight: 2 },
  { kind: "proportional", weight: 1 },
];

export const FIXTURE_GAP = 12;
export const FIXTURE_CONTAINER_WIDTH = 720;

/**
 * A small report grid:
 *   col 0: row label (fixed)
 *   col 1: description (content-sized)
 *   col 2/3: two proportional value columns
 */
export const FIXTURE_TREE: LayoutNode = {
  id: "root",
  column: 0,
  columnSpan: 4,
  minWidth: 0,
  children: [
    {
      id: "title",
      column: 0,
      columnSpan: 4,
      text: "Quarterly Sales Report",
      padding: 8,
    },
    { id: "row1-label", column: 0, text: "North", padding: 4 },
    { id: "row1-desc", column: 1, text: "Regional total", padding: 4 },
    { id: "row1-value", column: 2, text: "12800", padding: 4 },
    { id: "row1-delta", column: 3, text: "+12%", padding: 4 },
    { id: "row2-label", column: 0, text: "South", padding: 4 },
    { id: "row2-desc", column: 1, text: "WW exports", padding: 4 },
    { id: "row2-value", column: 2, text: "9600", padding: 4 },
    { id: "row2-delta", column: 3, text: "-3%", padding: 4 },
    {
      id: "summary-banner",
      column: 1,
      columnSpan: 2,
      text: "Summary: growth across all regions",
      padding: 8,
    },
  ],
};
