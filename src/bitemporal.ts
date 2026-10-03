/**
 * 双时态（bitemporal）存储表。
 *
 * 每条记录携带两个时间轴：
 *  - 业务时间 [validFrom, validTo)：事实在真实世界中生效的区间（半开区间）。
 *  - 系统时间 [sysFrom, sysTo)：系统知悉该事实的区间（半开区间），sysTo = Infinity 表示“当前认知”。
 *
 * 补录（retroactive insert）时，凡与新区间重叠的“当前认知”记录会被关闭（sysTo 置为录入时刻），
 * 其不重叠的残余部分以新的系统时间重新插入 —— 即自动分裂重叠时间段；
 * 已关闭的历史记录保持原样，从而完整保留审计轨迹。
 */

export interface BitemporalRecord<V> {
  readonly value: V;
  readonly validFrom: number;
  readonly validTo: number;
  readonly sysFrom: number;
  readonly sysTo: number;
}

interface MutableRecord<V> {
  value: V;
  validFrom: number;
  validTo: number;
  sysFrom: number;
  sysTo: number;
}

export class BitemporalTable<K, V> {
  private readonly records = new Map<K, MutableRecord<V>[]>();
  private clock = -Infinity;

  /** 在系统时刻 sysFrom 录入：key 在业务区间 [validFrom, validTo) 内取值为 value。 */
  insert(key: K, value: V, validFrom: number, validTo: number, sysFrom: number): void {
    if (!(validFrom < validTo)) {
      throw new RangeError(`业务区间非法：[${validFrom}, ${validTo}) 为空或反向`);
    }
    if (sysFrom < this.clock) {
      throw new RangeError(`系统时间不可倒流：${sysFrom} < ${this.clock}`);
    }
    this.clock = sysFrom;

    const rows = this.records.get(key) ?? [];
    const next: MutableRecord<V>[] = [];

    for (const row of rows) {
      const isCurrentBelief = row.sysTo === Infinity;
      const overlaps = row.validFrom < validTo && validFrom < row.validTo;
      if (!isCurrentBelief || !overlaps) {
        next.push(row);
        continue;
      }
      // 关闭旧认知，保留“当时系统认为如此”的历史。
      row.sysTo = sysFrom;
      next.push(row);
      // 分裂：旧记录不与新区间重叠的残余部分，以新系统时间重新生效。
      if (row.validFrom < validFrom) {
        next.push({ value: row.value, validFrom: row.validFrom, validTo: validFrom, sysFrom, sysTo: Infinity });
      }
      if (validTo < row.validTo) {
        next.push({ value: row.value, validFrom: validTo, validTo: row.validTo, sysFrom, sysTo: Infinity });
      }
    }

    next.push({ value, validFrom, validTo, sysFrom, sysTo: Infinity });
    this.records.set(key, next);
  }

  /** 查询：在系统知悉时刻 sysTime 看来，业务时刻 validTime 的取值。 */
  recordAt(key: K, validTime: number, sysTime: number): BitemporalRecord<V> | undefined {
    return (this.records.get(key) ?? []).find(
      (r) =>
        r.validFrom <= validTime && validTime < r.validTo &&
        r.sysFrom <= sysTime && sysTime < r.sysTo,
    );
  }

  valueAt(key: K, validTime: number, sysTime: number): V | undefined {
    return this.recordAt(key, validTime, sysTime)?.value;
  }

  /** 系统知悉时刻 sysTime 下，key 的全部“当时生效”记录（按业务时间排序）。 */
  timelineAt(key: K, sysTime: number): BitemporalRecord<V>[] {
    return (this.records.get(key) ?? [])
      .filter((r) => r.sysFrom <= sysTime && sysTime < r.sysTo)
      .sort((a, b) => a.validFrom - b.validFrom);
  }

  /** 完整审计轨迹（含已关闭记录），按系统时间、业务时间排序。 */
  history(key: K): BitemporalRecord<V>[] {
    return [...(this.records.get(key) ?? [])].sort(
      (a, b) => a.sysFrom - b.sysFrom || a.validFrom - b.validFrom,
    );
  }
}
