import type { ClientController } from './controller.ts';
import { projectGap } from './projection.ts';
import type { DragSession } from './session.ts';

/**
 * 键盘适配器：
 *   方向键移动焦点；Space 抬起节点；拖动中方向键移动插入线（gap），
 *   [ / ] 降低/提升缩进层级（depth）；Enter 提交；Escape 取消。
 * 与鼠标共用 projectGap，同一 (gap, depth) 产生同一逻辑操作。
 */
export class KeyboardAdapter {
  private c: ClientController;
  private session: DragSession | null = null;
  private gap = 0;
  private depth = 0;

  constructor(controller: ClientController) {
    this.c = controller;
  }

  key(key: string): void {
    if (this.session) this.keyWhileDragging(key);
    else this.keyWhileIdle(key);
  }

  private keyWhileIdle(key: string): void {
    const rows = this.c.rows();
    if (rows.length === 0) return;
    const focusIndex = Math.max(0, rows.findIndex((r) => r.id === this.c.focusId));
    if (key === 'ArrowDown') {
      this.c.setFocus(rows[Math.min(rows.length - 1, focusIndex + 1)].id);
    } else if (key === 'ArrowUp') {
      this.c.setFocus(rows[Math.max(0, focusIndex - 1)].id);
    } else if (key === ' ') {
      const source = rows[focusIndex];
      this.session = this.c.beginSession('keyboard', source.id);
      this.gap = focusIndex;
      this.depth = source.depth;
      this.emit();
    }
  }

  private keyWhileDragging(key: string): void {
    const rows = this.c.rows();
    switch (key) {
      case 'ArrowDown':
        this.gap = Math.min(rows.length, this.gap + 1);
        this.emit();
        break;
      case 'ArrowUp':
        this.gap = Math.max(0, this.gap - 1);
        this.emit();
        break;
      case ']':
        this.depth += 1;
        this.emit();
        break;
      case '[':
        this.depth = Math.max(0, this.depth - 1);
        this.emit();
        break;
      case 'Enter':
        this.c.commit(this.session as DragSession);
        this.session = null;
        break;
      case 'Escape':
        this.c.cancel(this.session as DragSession);
        this.session = null;
        break;
    }
  }

  private emit(): void {
    if (!this.session || this.session.sourceId === null) return;
    const spec = projectGap(this.c.rows(), this.session.sourceId, this.gap, this.depth);
    if (spec) this.c.hover(this.session, spec);
  }
}
