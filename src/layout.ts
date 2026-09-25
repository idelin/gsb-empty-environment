/**
 * Orchestration: flatten the fixture node tree into grid items (measuring
 * text with the fixture glyph metrics) and run the track sizing engine.
 */

import { FIXTURE_GLYPH_METRICS, type GlyphMetricsTable } from "./glyph-metrics.ts";
import { collectItems, type LayoutNode } from "./tree.ts";
import {
  sizeTracks,
  type TrackDefinition,
  type TrackSizingResult,
} from "./tracks.ts";

export interface LayoutInput {
  readonly tree: LayoutNode;
  readonly tracks: readonly TrackDefinition[];
  readonly containerWidth: number;
  readonly gap?: number;
  readonly metrics?: GlyphMetricsTable;
}

export function layout(input: LayoutInput): TrackSizingResult {
  const metrics = input.metrics ?? FIXTURE_GLYPH_METRICS;
  const items = collectItems(input.tree, metrics);
  return sizeTracks({
    tracks: input.tracks,
    items,
    containerWidth: input.containerWidth,
    gap: input.gap,
  });
}
