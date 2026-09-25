// Orchestrates track sizing and places items on the resolved grid.

import type { LayoutContainer } from "./tree.ts";
import { validateContainer } from "./tree.ts";
import { sizeTracks, type OverflowDiagnostic } from "./tracks.ts";
import { measureText, type FontMetrics } from "./glyphs.ts";
import { fixtureFont } from "./glyphs.ts";

export interface ItemRect {
  id: string;
  x: number;
  width: number;
  // Full text width at the item's font size (may exceed the rect when overflowing).
  textWidth: number;
}

export interface LayoutResult {
  trackSizes: number[];
  items: ItemRect[];
  diagnostics: OverflowDiagnostic[];
  fractionIterations: number;
}

export function layoutContainer(
  container: LayoutContainer,
  font: FontMetrics = fixtureFont,
): LayoutResult {
  validateContainer(container);
  const { sizes, diagnostics, fractionIterations } = sizeTracks(container, font);

  const offsets: number[] = [];
  let cursor = 0;
  for (let i = 0; i < sizes.length; i++) {
    offsets.push(cursor);
    cursor += sizes[i] + container.gap;
  }

  const items: ItemRect[] = container.items.map((item) => {
    const end = item.column + item.columnSpan;
    let width = container.gap * (item.columnSpan - 1);
    for (let i = item.column; i < end; i++) width += sizes[i];
    return {
      id: item.id,
      x: offsets[item.column],
      width,
      textWidth: measureText(item.text, item.fontSize, font),
    };
  });

  return { trackSizes: sizes, items, diagnostics, fractionIterations };
}
