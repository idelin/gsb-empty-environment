/** Mouse driver: pointer events -> the same logical intents as the
 *  keyboard driver. Hit-testing uses fixture metrics only. */
import { ListClient } from '../client.ts';
import { computeLayout, dropLocationAt, hitTestNode, type GlyphMetrics } from '../layout.ts';

export class MouseDriver {
  private client: ListClient;
  private metrics: GlyphMetrics;

  constructor(client: ListClient, metrics: GlyphMetrics) {
    this.client = client;
    this.metrics = metrics;
  }

  pointerDown(x: number, y: number): void {
    const id = hitTestNode(computeLayout(this.client.tree, this.metrics), x, y);
    if (id !== null) this.client.startDrag(id);
  }

  pointerMove(x: number, y: number): void {
    if (this.client.machine.phase !== 'dragging') return;
    const loc = dropLocationAt(this.client.tree, this.metrics, x, y);
    if (loc) this.client.dragTo({ parentId: loc.parentId, index: loc.index });
  }

  pointerUp(): void {
    this.client.commitDrag();
  }

  /** Mouse-side cancel (e.g. Escape or dropping outside the list). */
  cancel(): void {
    this.client.cancelDrag();
  }
}
