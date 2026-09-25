// Incremental truncation engine. Glyph metrics are versioned; when a
// new table arrives (e.g. the real font finished loading) the layout is
// recomputed, while stale tables and out-of-order async responses are
// rejected so an old measurement can never overwrite a newer layout.

import type { BlockInput, LayoutOptions, LayoutResult, MetricTable, TruncationMode } from './types.ts';
import { flattenBlock } from './flatten.ts';
import { layoutBlock } from './layout.ts';

export interface ApplyResult {
  applied: boolean;
  reason?: string;
  layout: LayoutResult;
}

export class TruncationEngine {
  source: string;
  paraDir: 'ltr' | 'rtl';
  items: import('./types.ts').ClusterItem[];
  metrics: MetricTable;
  width: number;
  maxLines: number;
  mode: TruncationMode;
  lastLayout: LayoutResult;
  requestSeq = 0; // monotonic token guarding async metric deliveries

  constructor(block: BlockInput, metrics: MetricTable, opts: LayoutOptions) {
    const flat = flattenBlock(block);
    this.source = flat.source;
    this.paraDir = flat.paraDir;
    this.items = flat.items;
    this.metrics = metrics;
    this.width = opts.width;
    this.maxLines = opts.maxLines;
    this.mode = opts.mode;
    this.lastLayout = this.relayout();
  }

  relayout(): LayoutResult {
    this.lastLayout = layoutBlock(this.items, this.paraDir, this.source, {
      width: this.width,
      maxLines: this.maxLines,
      mode: this.mode,
    }, this.metrics);
    return this.lastLayout;
  }

  // Synchronous metric application. A table whose version is not newer
  // than the current one is rejected and the layout is left untouched.
  applyMetrics(table: MetricTable): ApplyResult {
    if (table.version <= this.metrics.version) {
      return { applied: false, reason: 'stale-version', layout: this.lastLayout };
    }
    this.metrics = table;
    return { applied: true, layout: this.relayout() };
  }

  // Asynchronous metric delivery (late font metrics). Each call gets a
  // fresh token; when the promise settles, the table is applied only if
  // no newer request has been issued since.
  async deliverMetrics(promise: Promise<MetricTable>): Promise<ApplyResult> {
    const seq = ++this.requestSeq;
    const table = await promise;
    if (seq !== this.requestSeq) {
      return { applied: false, reason: 'superseded-request', layout: this.lastLayout };
    }
    return this.applyMetrics(table);
  }

  setWidth(width: number): LayoutResult {
    this.width = width;
    return this.relayout();
  }

  setMaxLines(maxLines: number): LayoutResult {
    this.maxLines = maxLines;
    return this.relayout();
  }

  setMode(mode: TruncationMode): LayoutResult {
    this.mode = mode;
    return this.relayout();
  }
}
