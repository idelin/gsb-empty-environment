import { AbstractStore } from "./store.ts";
import type { QueryResult, Revision } from "./types.ts";

interface FileIndex<A> {
  /**
   * 按 (knowledgeVersion, revisionId) 升序。
   * 录入版本单调不减且修订 ID 递增，因此录入（含迟到补录）只需尾部追加；
   * 撤销通过 revisionsById 原地更新 revokedAt，无需调整结构。
   */
  readonly byKnowledge: Revision<A>[];
}

/**
 * 可更新索引实现：按文件维护录入版本有序序列，
 * 查询先二分截取出 knowledgeVersion <= knowledgeTime 的前缀，再在前缀内按
 * 业务区间与撤销状态过滤决胜。
 */
export class IndexedStore<A = Record<string, unknown>> extends AbstractStore<A> {
  private readonly files = new Map<string, FileIndex<A>>();

  protected onRecord(revision: Revision<A>): void {
    let index = this.files.get(revision.fileId);
    if (!index) {
      index = { byKnowledge: [] };
      this.files.set(revision.fileId, index);
    }
    index.byKnowledge.push(revision);
  }

  query(fileId: string, validTime: number, knowledgeTime: number): QueryResult<A> | null {
    const index = this.files.get(fileId);
    if (!index) return null;
    const entries = index.byKnowledge;
    // 二分：右边界为最后一个 knowledgeVersion <= knowledgeTime 的位置
    let lo = 0;
    let hi = entries.length;
    while (lo < hi) {
      const mid = (lo + hi) >>> 1;
      if (entries[mid].knowledgeVersion <= knowledgeTime) {
        lo = mid + 1;
      } else {
        hi = mid;
      }
    }
    let best: Revision<A> | null = null;
    for (let i = 0; i < lo; i++) {
      const revision = entries[i];
      if (!this.isVisible(revision, validTime, knowledgeTime)) continue;
      if (best === null || this.isBetter(revision, best)) {
        best = revision;
      }
    }
    return best === null ? null : this.toResult(best);
  }
}
