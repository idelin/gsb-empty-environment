/**
 * 双时态（bitemporal）存储内核。
 *
 * - valid time（业务/生效时间）：事实在现实世界中生效的区间 [validFrom, validTo)
 * - transaction time（记录/系统时间）：事实被系统知晓的区间 [txFrom, txTo)
 *
 * 所有记录只增不改；修正通过"关闭旧记录的事务时间 + 追加新记录"完成，
 * 因此可以回答"在任意记录时间点 tx，回看任意业务时间点 vt 的世界状态"。
 */

export const FAR_FUTURE = Number.MAX_SAFE_INTEGER;

export interface BitemporalRecord<T> {
  readonly id: number;
  readonly payload: T;
  readonly validFrom: number;
  validTo: number;
  readonly txFrom: number;
  txTo: number;
}

export class BitemporalStore<T> {
  private records: BitemporalRecord<T>[] = [];
  private nextId = 1;

  /** 追加一条事实，validFrom 为业务生效时间，txFrom 为系统记录时间。 */
  insert(payload: T, validFrom: number, txFrom: number, validTo: number = FAR_FUTURE): BitemporalRecord<T> {
    const record: BitemporalRecord<T> = {
      id: this.nextId++,
      payload,
      validFrom,
      validTo,
      txFrom,
      txTo: FAR_FUTURE,
    };
    this.records.push(record);
    return record;
  }

  /**
   * 追溯修正：把当前仍生效（txTo 未关闭）且匹配的记录在 txTime 处关闭，
   * 并以原业务生效时间插入新记录（默认保持原 validFrom/validTo，实现"追溯"）。
   */
  correct(
    predicate: (payload: T) => boolean,
    newPayload: T,
    txTime: number,
    validFrom?: number,
    validTo?: number,
  ): BitemporalRecord<T>[] {
    // 先快照匹配记录，避免插入的新记录进入迭代被再次关闭
    const matched = this.records.filter((r) => r.txTo === FAR_FUTURE && predicate(r.payload));
    const inserted: BitemporalRecord<T>[] = [];
    for (const record of matched) {
      record.txTo = txTime;
      inserted.push(
        this.insert(
          newPayload,
          validFrom ?? record.validFrom,
          txTime,
          validTo ?? record.validTo,
        ),
      );
    }
    return inserted;
  }

  /** 业务时间截断：把匹配且当前生效、覆盖 atValidTime 的记录在该处截断（用于同序列新估值接续旧估值）。 */
  truncateValid(predicate: (payload: T) => boolean, atValidTime: number): void {
    for (const record of this.records) {
      if (
        record.txTo === FAR_FUTURE &&
        predicate(record.payload) &&
        record.validFrom < atValidTime &&
        record.validTo > atValidTime
      ) {
        record.validTo = atValidTime;
      }
    }
  }

  /** 双时态查询：业务时间 vt 生效、且在记录时间 tt 时系统已知晓的全部记录。 */
  asOf(validTime: number, txTime: number): BitemporalRecord<T>[] {
    return this.records.filter(
      (r) => r.validFrom <= validTime && validTime < r.validTo && r.txFrom <= txTime && txTime < r.txTo,
    );
  }

  /** 完整历史（含已被修正关闭的记录），用于审计追溯。 */
  history(): readonly BitemporalRecord<T>[] {
    return this.records;
  }
}

/** 从 asOf 结果中取业务生效时间最新的一条（同一时点的最新事实）。 */
export function latest<T>(records: BitemporalRecord<T>[]): BitemporalRecord<T> | undefined {
  let best: BitemporalRecord<T> | undefined;
  for (const record of records) {
    if (!best || record.validFrom > best.validFrom) best = record;
  }
  return best;
}
