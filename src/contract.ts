/**
 * 双时态合同履约状态机
 *
 * 两个时间轴：
 *  - 业务时间 (valid time)：事件在业务上发生的时间（如交付发生日、合同期间）。
 *  - 知悉时间 (transaction time)：系统录入/获知该事件的时间。
 *
 * 合同状态沿业务时间演进：DRAFT(草案) -> EFFECTIVE(生效) -> IN_PROGRESS(履行中) -> TERMINATED(终止)。
 * 交付凭证支持补交（以过去的业务时间录入）与作废（自某知悉时间起失效），
 * 并可在任意 (业务时间, 知悉时间) 双时态切片上实时计算履约完成度。
 */

export type ContractState = 'DRAFT' | 'EFFECTIVE' | 'IN_PROGRESS' | 'TERMINATED';

/** 一条交付凭证（双时态记录） */
export interface Delivery {
  readonly id: string;
  /** 业务时间：交付实际发生的业务日期 */
  readonly validTime: number;
  /** 交付数量 */
  readonly quantity: number;
  /** 知悉时间：录入系统的时刻 */
  readonly recordedAt: number;
  /** 知悉时间：作废时刻；null 表示仍然有效 */
  readonly voidedAt: number | null;
}

/** 双时态切片查询结果 */
export interface FulfillmentSnapshot {
  readonly validTime: number;
  readonly knowledgeTime: number;
  readonly state: ContractState;
  /** 履约完成度百分比，范围 [0, 100] */
  readonly completion: number;
  /** 截至该切片累计已履约数量 */
  readonly deliveredQuantity: number;
  /** 该切片下可见（已录入且未作废）的交付凭证 */
  readonly deliveries: readonly Delivery[];
}

export interface ContractTerms {
  readonly id: string;
  /** 业务期起点（含） */
  readonly periodStart: number;
  /** 业务期终点（不含），到达即终止 */
  readonly periodEnd: number;
  /** 生效时间（业务时间），默认等于 periodStart */
  readonly effectiveFrom?: number;
  /** 合同约定的总履约数量 */
  readonly totalQuantity: number;
}

export class BitemporalContract {
  readonly id: string;
  readonly periodStart: number;
  readonly periodEnd: number;
  readonly effectiveFrom: number;
  readonly totalQuantity: number;

  private readonly deliveries: Delivery[] = [];
  private nextDeliverySeq = 1;

  constructor(terms: ContractTerms) {
    if (!(terms.periodStart < terms.periodEnd)) {
      throw new RangeError('periodStart 必须小于 periodEnd');
    }
    if (!(terms.totalQuantity > 0)) {
      throw new RangeError('totalQuantity 必须为正数');
    }
    const effectiveFrom = terms.effectiveFrom ?? terms.periodStart;
    if (effectiveFrom < terms.periodStart || effectiveFrom >= terms.periodEnd) {
      throw new RangeError('effectiveFrom 必须落在业务期 [periodStart, periodEnd) 内');
    }
    this.id = terms.id;
    this.periodStart = terms.periodStart;
    this.periodEnd = terms.periodEnd;
    this.effectiveFrom = effectiveFrom;
    this.totalQuantity = terms.totalQuantity;
  }

  /**
   * 录入交付凭证。validTime 可以是过去（补交历史阶段的交付）。
   * @returns 新凭证 id
   */
  recordDelivery(knowledgeTime: number, validTime: number, quantity: number): string {
    if (validTime < this.periodStart || validTime >= this.periodEnd) {
      throw new RangeError(
        `交付业务时间 ${validTime} 不在合同业务期 [${this.periodStart}, ${this.periodEnd}) 内`,
      );
    }
    if (!(quantity > 0)) {
      throw new RangeError('交付数量必须为正数');
    }
    const id = `D${this.nextDeliverySeq++}`;
    this.deliveries.push({ id, validTime, quantity, recordedAt: knowledgeTime, voidedAt: null });
    return id;
  }

  /** 作废交付凭证：自 knowledgeTime 起，该凭证对后续知悉时间的查询不可见。 */
  voidDelivery(knowledgeTime: number, deliveryId: string): void {
    const delivery = this.deliveries.find((d) => d.id === deliveryId);
    if (!delivery) {
      throw new Error(`交付凭证不存在: ${deliveryId}`);
    }
    if (delivery.voidedAt !== null) {
      throw new Error(`交付凭证已被作废: ${deliveryId}`);
    }
    if (knowledgeTime < delivery.recordedAt) {
      throw new RangeError('作废的知悉时间不能早于录入的知悉时间');
    }
    (delivery as { voidedAt: number | null }).voidedAt = knowledgeTime;
  }

  /** 某知悉时间下可见的交付凭证（已录入且尚未作废），按业务时间排序。 */
  activeDeliveriesAt(knowledgeTime: number): Delivery[] {
    return this.deliveries
      .filter((d) => d.recordedAt <= knowledgeTime && (d.voidedAt === null || d.voidedAt > knowledgeTime))
      .slice()
      .sort((a, b) => a.validTime - b.validTime);
  }

  /** 双时态切片下的合同状态。 */
  stateAt(validTime: number, knowledgeTime: number): ContractState {
    if (validTime < this.effectiveFrom) return 'DRAFT';
    if (validTime >= this.periodEnd) return 'TERMINATED';
    const hasDelivery = this.activeDeliveriesAt(knowledgeTime).some((d) => d.validTime <= validTime);
    return hasDelivery ? 'IN_PROGRESS' : 'EFFECTIVE';
  }

  /**
   * 双时态切片下的履约完成度百分比 [0, 100]。
   * 统计该知悉时间可见、且业务时间不晚于 validTime 的交付数量之和。
   */
  completionAt(validTime: number, knowledgeTime: number): number {
    const delivered = this.deliveredQuantityAt(validTime, knowledgeTime);
    return Math.min(100, (delivered / this.totalQuantity) * 100);
  }

  /** 双时态切片下累计已履约数量。 */
  deliveredQuantityAt(validTime: number, knowledgeTime: number): number {
    return this.activeDeliveriesAt(knowledgeTime)
      .filter((d) => d.validTime <= validTime)
      .reduce((sum, d) => sum + d.quantity, 0);
  }

  /** 查询任意 (业务时间, 知悉时间) 切片的完整履约快照。 */
  snapshot(validTime: number, knowledgeTime: number): FulfillmentSnapshot {
    return {
      validTime,
      knowledgeTime,
      state: this.stateAt(validTime, knowledgeTime),
      completion: this.completionAt(validTime, knowledgeTime),
      deliveredQuantity: this.deliveredQuantityAt(validTime, knowledgeTime),
      deliveries: this.activeDeliveriesAt(knowledgeTime).filter((d) => d.validTime <= validTime),
    };
  }
}
