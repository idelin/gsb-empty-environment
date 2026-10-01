/**
 * 双时态合同履约状态机
 *
 * 两个时间维度：
 *  - 业务时间 (business time)：合同条款/交付在业务上生效的时间轴，合同业务期为 [validFrom, validTo)
 *  - 知悉时间 (transaction/knowledge time)：系统获知并录入事实的时间轴，只增不减
 *
 * 每份交付凭证携带一个知悉区间 [txFrom, txTo)：作废只是关闭区间而非删除记录，
 * 因此可以查询任意 (业务时间, 知悉时间) 双时态切片下的合同状态与履约完成度。
 */

export const ContractState = {
  Draft: 'DRAFT',             // 草案：业务时间尚未进入合同期
  Effective: 'EFFECTIVE',     // 生效：已进入合同期，但尚无可见交付
  Performing: 'PERFORMING',   // 履行中：已存在可见交付
  Terminated: 'TERMINATED',   // 终止：业务时间已越过合同期末
} as const;

export type ContractStateValue = (typeof ContractState)[keyof typeof ContractState];

export interface DeliveryVersion {
  readonly id: number;
  /** 交付发生的业务时间 */
  readonly businessTime: number;
  /** 交付数量 */
  readonly quantity: number;
  /** 录入时的知悉时间（含） */
  readonly txFrom: number;
  /** 作废时的知悉时间（不含）；Infinity 表示仍有效 */
  txTo: number;
}

export interface BitemporalSnapshot {
  businessTime: number;
  knowledgeTime: number;
  state: ContractStateValue;
  /** 履约完成度百分比，0-100 */
  completionPercent: number;
  /** 该切片下可见的交付凭证 */
  visibleDeliveries: ReadonlyArray<DeliveryVersion>;
}

export class BitemporalContract {
  readonly validFrom: number;
  readonly validTo: number;
  /** 合同要求交付的总量 */
  readonly requiredQuantity: number;

  private readonly versions: DeliveryVersion[] = [];
  private knowledgeClock = 0;
  private nextId = 1;

  constructor(validFrom: number, validTo: number, requiredQuantity: number) {
    if (!(validFrom < validTo)) {
      throw new Error(`合同业务期非法：[${validFrom}, ${validTo})`);
    }
    if (!(requiredQuantity > 0)) {
      throw new Error(`要求交付总量必须为正数，收到 ${requiredQuantity}`);
    }
    this.validFrom = validFrom;
    this.validTo = validTo;
    this.requiredQuantity = requiredQuantity;
  }

  /** 当前知悉时钟 */
  get knowledgeTime(): number {
    return this.knowledgeClock;
  }

  /** 推进知悉时钟（只增不减） */
  advanceKnowledgeTime(to: number): void {
    if (to <= this.knowledgeClock) {
      throw new Error(`知悉时间必须单调递增：当前 ${this.knowledgeClock}，收到 ${to}`);
    }
    this.knowledgeClock = to;
  }

  /** 在当前知悉时间录入一份交付凭证（支持补交历史业务时间的交付） */
  recordDelivery(businessTime: number, quantity: number): number {
    if (businessTime < this.validFrom || businessTime >= this.validTo) {
      throw new Error(`交付业务时间 ${businessTime} 不在合同业务期 [${this.validFrom}, ${this.validTo}) 内`);
    }
    if (!(quantity > 0)) {
      throw new Error(`交付数量必须为正数，收到 ${quantity}`);
    }
    const version: DeliveryVersion = {
      id: this.nextId++,
      businessTime,
      quantity,
      txFrom: this.knowledgeClock,
      txTo: Infinity,
    };
    this.versions.push(version);
    return version.id;
  }

  /** 在当前知悉时间作废一份交付凭证（关闭其知悉区间，历史切片仍可见） */
  voidDelivery(id: number): void {
    const live = this.versions.find((v) => v.id === id && v.txTo === Infinity);
    if (!live) {
      throw new Error(`交付凭证 #${id} 不存在或已作废`);
    }
    live.txTo = this.knowledgeClock;
  }

  /** 某知悉时间下可见的交付版本：txFrom <= kt < txTo */
  deliveriesAsOf(knowledgeTime: number): DeliveryVersion[] {
    return this.versions.filter(
      (v) => v.txFrom <= knowledgeTime && knowledgeTime < v.txTo,
    );
  }

  /** 双时态切片下的合同状态 */
  stateAt(businessTime: number, knowledgeTime: number): ContractStateValue {
    if (businessTime < this.validFrom) return ContractState.Draft;
    if (businessTime >= this.validTo) return ContractState.Terminated;
    const hasVisibleDelivery = this.deliveriesAsOf(knowledgeTime).some(
      (v) => v.businessTime <= businessTime,
    );
    return hasVisibleDelivery ? ContractState.Performing : ContractState.Effective;
  }

  /** 双时态切片下的履约完成度百分比（0-100，封顶 100） */
  completionAt(businessTime: number, knowledgeTime: number): number {
    const delivered = this.deliveriesAsOf(knowledgeTime)
      .filter((v) => v.businessTime <= businessTime)
      .reduce((sum, v) => sum + v.quantity, 0);
    return Math.min(100, (delivered / this.requiredQuantity) * 100);
  }

  /** 任意双时态切片的完整快照 */
  snapshotAt(businessTime: number, knowledgeTime: number): BitemporalSnapshot {
    return {
      businessTime,
      knowledgeTime,
      state: this.stateAt(businessTime, knowledgeTime),
      completionPercent: this.completionAt(businessTime, knowledgeTime),
      visibleDeliveries: this.deliveriesAsOf(knowledgeTime).filter(
        (v) => v.businessTime <= businessTime,
      ),
    };
  }
}
