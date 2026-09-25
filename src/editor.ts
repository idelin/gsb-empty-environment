/**
 * PixelEditor：对外门面。
 * - stroke/fillRect：局部像素编辑，只把变化像素存入差量事务；
 * - crop：尺寸变化作为结构事务纳入同一棵历史树，可撤销恢复被裁像素；
 * - undo/redo：支持分支；redo 可指定分支事务 id；
 * - 历史容量超限自动压缩淘汰，不影响当前图。
 */
import { RGBAImage, type Color } from './image.ts';
import {
  History,
  Transaction,
  diffTransaction,
  snapshotTransaction,
  applyTransaction,
  revertTransaction,
  type BranchInfo,
  type HistoryStats,
} from './history.ts';
export type { BranchInfo, HistoryStats };

export interface EditorOptions {
  /** 历史树中最多保留的活事务数，超出后自动淘汰（剪死分支/前滚合并进基线） */
  maxTransactions?: number;
}

export class PixelEditor {
  image: RGBAImage;
  private history: History;

  constructor(width: number, height: number, options: EditorOptions = {}) {
    this.image = new RGBAImage(width, height);
    this.history = new History(this.image, options.maxTransactions ?? 128);
  }

  /** 用指定颜色绘制一组像素点（一个笔触 = 一个事务）。 */
  stroke(points: Iterable<[number, number]>, color: Color, label = 'stroke'): Transaction {
    const before = this.image.clone();
    for (const [x, y] of points) {
      if (x >= 0 && y >= 0 && x < this.image.width && y < this.image.height) {
        this.image.setPixel(x, y, color);
      }
    }
    return this.commitDiff(label, before);
  }

  fillRect(x: number, y: number, w: number, h: number, color: Color, label = 'fillRect'): Transaction {
    const before = this.image.clone();
    for (let row = y; row < y + h; row++) {
      for (let col = x; col < x + w; col++) {
        if (col >= 0 && row >= 0 && col < this.image.width && row < this.image.height) {
          this.image.setPixel(col, row, color);
        }
      }
    }
    return this.commitDiff(label, before);
  }

  /** 裁剪：尺寸变化纳入事务，撤销可恢复被裁掉的像素。 */
  crop(x: number, y: number, w: number, h: number, label = 'crop'): Transaction {
    const before = this.image.clone();
    const after = before.crop(x, y, w, h);
    const tx = snapshotTransaction(this.history.allocateTransactionId(), label, before, after);
    this.image = after;
    this.history.commit(tx);
    return tx;
  }

  undo(): boolean {
    const tx = this.history.undo();
    if (!tx) return false;
    this.image = revertTransaction(this.image, tx);
    return true;
  }

  /** 重做；有多个分支时可用 transactionId 选择（见 branches()）。 */
  redo(transactionId?: number): boolean {
    const tx = this.history.redo(transactionId);
    if (!tx) return false;
    this.image = applyTransaction(this.image, tx);
    return true;
  }

  /** 当前位置可重做的分支。 */
  branches(): BranchInfo[] {
    return this.history.branches();
  }

  stats(): HistoryStats {
    return this.history.stats();
  }

  /** 从基线完整重放，用于验证淘汰未损坏当前图。 */
  verifyAgainstReplay(): boolean {
    return this.image.equals(this.history.replayToHead());
  }

  private commitDiff(label: string, before: RGBAImage): Transaction {
    const tx = diffTransaction(this.history.allocateTransactionId(), label, before, this.image);
    this.history.commit(tx);
    return tx;
  }
}
