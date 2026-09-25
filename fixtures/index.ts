import nodeTreeJson from './node-tree.json' with { type: 'json' };
import glyphMetricsJson from './glyph-metrics.json' with { type: 'json' };
import type { GlyphMetrics, LayoutNode } from '../src/layout.ts';

export const nodeTree = nodeTreeJson as LayoutNode;
export const glyphMetrics = glyphMetricsJson as GlyphMetrics;
