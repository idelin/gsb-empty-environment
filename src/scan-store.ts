import { AbstractStore } from "./store.ts";
import type { QueryResult, Revision } from "./types.ts";

/**
 * 逐条扫描参考实现：不做任何索引，查询时全量扫描。
 * 用作索引实现的正确性核对基准。
 */
export class ScanStore<A = Record<string, unknown>> extends AbstractStore<A> {
  private readonly all: Revision<A>[] = [];

  protected onRecord(revision: Revision<A>): void {
    this.all.push(revision);
  }

  query(fileId: string, validTime: number, knowledgeTime: number): QueryResult<A> | null {
    let best: Revision<A> | null = null;
    for (const revision of this.all) {
      if (revision.fileId !== fileId) continue;
      if (!this.isVisible(revision, validTime, knowledgeTime)) continue;
      if (best === null || this.isBetter(revision, best)) {
        best = revision;
      }
    }
    return best === null ? null : this.toResult(best);
  }
}
