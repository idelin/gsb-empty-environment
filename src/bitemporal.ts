/**
 * 双时态（bitemporal）存储内核。
 *
 * 每条记录携带两个时间维度：
 * - validFrom / validTo：业务生效时间（valid time），描述事实在业务世界中成立的时间段；
 * - recordedAt：系统登记时间（transaction time），描述系统何时得知该事实。
 *
 * 修正历史（如追溯调整借款金额）不会覆盖旧记录，而是以更晚的 recordedAt
 * 追加一条同 key 的新记录。查询时同时指定 validTime 与 txTime，
 * 即可重现"在 txTime 时点系统所认知的、validTime 时点的业务状态"。
 */

export interface BitemporalRecord<T> {
  readonly key: string;
  readonly validFrom: number;
  readonly validTo: number;
  readonly recordedAt: number;
  readonly payload: T;
}

export class BitemporalStore<T> {
  private readonly records: BitemporalRecord<T>[] = [];

  record(entry: BitemporalRecord<T>): void {
    if (!(entry.validTo > entry.validFrom)) {
      throw new Error(
        `无效的有效期区间 [${entry.validFrom}, ${entry.validTo})：validTo 必须大于 validFrom`,
      );
    }
    this.records.push(entry);
  }

  /**
   * 双时态点查：在 txTime 时点系统已知的记录中，
   * 找出覆盖 validTime 的、登记时间最新的一条。
   */
  asOf(key: string, validTime: number, txTime: number): BitemporalRecord<T> | undefined {
    let best: BitemporalRecord<T> | undefined;
    for (const rec of this.records) {
      if (rec.key !== key) continue;
      if (rec.recordedAt > txTime) continue;
      if (rec.validFrom > validTime || rec.validTo <= validTime) continue;
      if (best === undefined || rec.recordedAt > best.recordedAt) {
        best = rec;
      }
    }
    return best;
  }

  /** 指定 key（或全部）记录按登记时间排序的完整履历，用于审计追溯。 */
  history(key?: string): readonly BitemporalRecord<T>[] {
    const list = key === undefined ? this.records : this.records.filter((r) => r.key === key);
    return [...list].sort((a, b) => a.recordedAt - b.recordedAt);
  }
}
