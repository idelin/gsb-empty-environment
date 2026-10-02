/**
 * 双时态（bitemporal）关系存储。
 *
 * 每条记录携带两个时间维度：
 * - 业务时间 [validFrom, validTo)：事实在真实世界生效的时间段（生效时间段）。
 * - 系统时间 [sysFrom, sysTo)：该记录被系统知悉/录入的时间段（录入时间）。
 *
 * 所有区间均为左闭右开。OPEN 表示区间尚未封闭（至今有效 / 至今仍被知悉）。
 *
 * 修订（含补录历史事件）不会删除旧记录，而是：
 * 1. 将与新记录业务时间段重叠、且当前仍被知悉的旧记录在 sysTime 处封闭（sysTo = sysTime）；
 * 2. 把旧记录未被覆盖的前段 / 后段以 sysFrom = sysTime 重新写入（自动分裂）；
 * 3. 追加新记录。
 * 因此任意 (业务时刻, 知悉时刻) 组合的历史视图都可重建，全程可审计。
 */

export const OPEN = Number.POSITIVE_INFINITY;

export interface BitemporalRecord<V> {
  readonly subject: string;
  readonly value: V;
  readonly validFrom: number;
  readonly validTo: number;
  readonly sysFrom: number;
  sysTo: number;
}

const overlaps = (
  aFrom: number,
  aTo: number,
  bFrom: number,
  bTo: number,
): boolean => aFrom < bTo && bFrom < aTo;

const covers = (from: number, to: number, point: number): boolean =>
  from <= point && point < to;

export class BitemporalRelations<V> {
  private readonly records: BitemporalRecord<V>[] = [];

  /**
   * 在 sysTime 录入一条事实：subject 在业务时间 [validFrom, validTo) 内取值为 value。
   * 与当前已知记录重叠的部分会被自动分裂，重叠段由新记录取代。
   */
  set(
    subject: string,
    value: V,
    validFrom: number,
    validTo: number,
    sysTime: number,
  ): void {
    if (!(validFrom < validTo)) {
      throw new RangeError(
        `无效的业务时间段 [${validFrom}, ${validTo})：validFrom 必须小于 validTo`,
      );
    }

    for (const record of this.records) {
      if (record.subject !== subject) continue;
      if (record.sysTo !== OPEN) continue;
      if (!overlaps(record.validFrom, record.validTo, validFrom, validTo)) {
        continue;
      }

      record.sysTo = sysTime;

      if (record.validFrom < validFrom) {
        this.records.push({
          subject,
          value: record.value,
          validFrom: record.validFrom,
          validTo: validFrom,
          sysFrom: sysTime,
          sysTo: OPEN,
        });
      }
      if (record.validTo > validTo) {
        this.records.push({
          subject,
          value: record.value,
          validFrom: validTo,
          validTo: record.validTo,
          sysFrom: sysTime,
          sysTo: OPEN,
        });
      }
    }

    this.records.push({
      subject,
      value,
      validFrom,
      validTo,
      sysFrom: sysTime,
      sysTo: OPEN,
    });
  }

  /**
   * 查询在 knowledgeTime 时刻系统所知的、subject 在 businessTime 业务时刻的取值。
   */
  valueAt(
    subject: string,
    businessTime: number,
    knowledgeTime: number,
  ): V | undefined {
    for (const record of this.records) {
      if (record.subject !== subject) continue;
      if (!covers(record.validFrom, record.validTo, businessTime)) continue;
      if (!covers(record.sysFrom, record.sysTo, knowledgeTime)) continue;
      return record.value;
    }
    return undefined;
  }

  /** 返回某主体的全部记录（含已封闭的历史版本），用于审计追溯。 */
  auditTrail(subject: string): readonly BitemporalRecord<V>[] {
    return this.records
      .filter((record) => record.subject === subject)
      .map((record) => ({ ...record }))
      .sort(
        (a, b) =>
          a.validFrom - b.validFrom ||
          a.sysFrom - b.sysFrom ||
          a.validTo - b.validTo,
      );
  }
}
