// Fixture node tree used by the demo and the tests.

import type { LayoutContainer } from "./tree.ts";

// A mixed grid: fixed sidebar, content-sized column, two fractions,
// with an item spanning the two fraction columns.
export const mixedGrid: LayoutContainer = {
  width: 800,
  gap: 10,
  columns: [
    { kind: "fixed", px: 160 },
    { kind: "content" },
    { kind: "fraction", weight: 2 },
    { kind: "fraction", weight: 1 },
  ],
  items: [
    { id: "logo", column: 0, columnSpan: 1, text: "Grid", fontSize: 16 },
    { id: "nav", column: 1, columnSpan: 1, text: "alpha beta", fontSize: 16 },
    { id: "hero", column: 2, columnSpan: 2, text: "Gamma delta epsilon", fontSize: 16 },
    { id: "side", column: 2, columnSpan: 1, text: "one", fontSize: 16 },
    { id: "foot", column: 3, columnSpan: 1, text: "two", fontSize: 16 },
  ],
};

// A container too small for its fixed tracks: must report overflow,
// never negative widths.
export const crampedGrid: LayoutContainer = {
  width: 100,
  gap: 10,
  columns: [
    { kind: "fixed", px: 80 },
    { kind: "fixed", px: 60 },
    { kind: "fraction", weight: 1 },
  ],
  items: [{ id: "a", column: 0, columnSpan: 1, text: "abc", fontSize: 16 }],
};

// Zero-weight fractions: the distributor must not divide by zero.
export const zeroWeightGrid: LayoutContainer = {
  width: 500,
  gap: 0,
  columns: [
    { kind: "fraction", weight: 0 },
    { kind: "fraction", weight: 0, min: 40 },
    { kind: "fixed", px: 100 },
  ],
  items: [],
};

// A wide item spanning two fraction columns: its minimum width must
// constrain the total grid width. Unequal weights force the iterative
// distributor to freeze the smaller track at its floor.
export const spanningGrid: LayoutContainer = {
  width: 400,
  gap: 20,
  columns: [
    { kind: "fraction", weight: 3 },
    { kind: "fraction", weight: 1 },
  ],
  items: [
    // min-content width = (540 + 25 * 800) / 1000 * 16 = 328.64px
    { id: "wide", column: 0, columnSpan: 2, text: "ommmmmmmmmmmmmmmmmmmmmmmmm", fontSize: 16 },
    { id: "c0", column: 0, columnSpan: 1, text: "a", fontSize: 16 },
    { id: "c1", column: 1, columnSpan: 1, text: "b", fontSize: 16 },
  ],
};
