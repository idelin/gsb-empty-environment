import type { ClientController } from './controller.ts';
import type { GlyphMetrics } from './metrics.ts';
import { depthAt, gapIndexAt, layoutRows } from './metrics.ts';
import { projectGap } from './projection.ts';
import type { DragSession } from './session.ts';

/** 鼠标适配器：把指针坐标换算成 (gap, depth)，走统一投影产生逻辑操作 */
export class MouseAdapter {
  private c: ClientController;
  private metrics: GlyphMetrics;
  private session: DragSession | null = null;

  constructor(controller: ClientController, metrics: GlyphMetrics) {
    this.c = controller;
    this.metrics = metrics;
  }

  pointerDown(_x: number, y: number): boolean {
    const rows = this.c.rows();
    const boxes = layoutRows(rows, this.metrics);
    const index = boxes.findIndex((b) => y >= b.top && y < b.bottom);
    if (index < 0) return false;
    this.session = this.c.beginSession('mouse', rows[index].id);
    return true;
  }

  pointerMove(x: number, y: number): void {
    if (!this.session || this.session.sourceId === null) return;
    const rows = this.c.rows();
    const gap = gapIndexAt(layoutRows(rows, this.metrics), y);
    const spec = projectGap(rows, this.session.sourceId, gap, depthAt(x, this.metrics));
    if (spec) this.c.hover(this.session, spec);
  }

  pointerUp(): void {
    if (!this.session) return;
    this.c.commit(this.session);
    this.session = null;
  }

  cancel(): void {
    if (!this.session) return;
    this.c.cancel(this.session);
    this.session = null;
  }
}
