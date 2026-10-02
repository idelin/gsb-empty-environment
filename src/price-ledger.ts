/**
 * 双时态价格规则引擎（Bitemporal Price Ledger）
 *
 * 每条价格规则携带两个时间维度：
 *  - Valid Time（业务生效时间）：价格在业务上何时生效，左闭右开区间 [validFrom, validTo)
 *  - Transaction Time（系统知悉时间）：系统何时知悉该规则，左闭右开区间 [txFrom, txTo)
 *
 * 语义：
 *  - 同一知悉时刻下，业务时间重叠的规则按“后入为主”（后知悉的规则覆盖先知悉的）。
 *  - 新规则登记时，仅对重叠部分进行覆盖：旧规则当前开放（txTo = ∞）的记录在
 *    新规则的 txTime 处关闭，未重叠的残余部分以新的知悉区间重新开放。
 *  - 撤销（revoke）只把规则当前开放的知悉区间在撤销时刻关闭，
 *    因此只影响知悉时间 >= 撤销时刻的查询，历史知悉视角保持不变。
 *  - 金额一律使用整数分（cents）。
 */

export const FOREVER = Number.POSITIVE_INFINITY;

/** 账本中一条历史切片。 */
export interface PriceSegment {
  readonly ruleId: number;
  readonly priceCents: number;
  readonly validFrom: number;
  readonly validTo: number;
  readonly txFrom: number;
  readonly txTo: number;
}

interface MutableSegment {
  ruleId: number;
  priceCents: number;
  validFrom: number;
  validTo: number;
  txFrom: number;
  txTo: number;
}

function assertNonNegativeInteger(value: number, name: string): void {
  if (!Number.isInteger(value) || value < 0) {
    throw new RangeError(`${name} 必须是非负整数，收到: ${value}`);
  }
}

export class BitemporalPriceLedger {
  private segments: MutableSegment[] = [];
  private nextRuleId = 1;
  private lastTxTime: number | null = null;

  /**
   * 登记一条价格规则。
   *
   * @param priceCents 价格（整数分）
   * @param validFrom  业务生效起点（含）
   * @param validTo    业务生效终点（不含）
   * @param txTime     系统知悉时刻
   * @returns 规则 ID，可用于后续撤销
   */
  insert(priceCents: number, validFrom: number, validTo: number, txTime: number): number {
    assertNonNegativeInteger(priceCents, 'priceCents');
    assertNonNegativeInteger(validFrom, 'validFrom');
    assertNonNegativeInteger(validTo, 'validTo');
    assertNonNegativeInteger(txTime, 'txTime');
    if (validFrom >= validTo) {
      throw new RangeError(`业务区间必须满足 validFrom < validTo，收到: [${validFrom}, ${validTo})`);
    }
    this.assertMonotonicTxTime(txTime);

    // 关闭与新区间重叠的开放记录，并把未重叠的残余部分以新知悉区间重新开放。
    for (const seg of [...this.segments]) {
      if (seg.txTo !== FOREVER) continue;
      if (seg.validFrom >= validTo || seg.validTo <= validFrom) continue;

      seg.txTo = txTime;
      if (seg.validFrom < validFrom) {
        this.segments.push({
          ruleId: seg.ruleId,
          priceCents: seg.priceCents,
          validFrom: seg.validFrom,
          validTo: Math.min(validFrom, seg.validTo),
          txFrom: txTime,
          txTo: FOREVER,
        });
      }
      if (seg.validTo > validTo) {
        this.segments.push({
          ruleId: seg.ruleId,
          priceCents: seg.priceCents,
          validFrom: Math.max(validTo, seg.validFrom),
          validTo: seg.validTo,
          txFrom: txTime,
          txTo: FOREVER,
        });
      }
    }

    const ruleId = this.nextRuleId++;
    this.segments.push({ ruleId, priceCents, validFrom, validTo, txFrom: txTime, txTo: FOREVER });
    this.lastTxTime = txTime;
    return ruleId;
  }

  /**
   * 撤销一条规则：把它所有当前开放的知悉区间在 txTime 处关闭。
   * 只影响知悉时间 >= txTime 的查询；不会复活被其他规则覆盖的旧价格。
   *
   * @returns 是否存在被关闭的开放区间
   */
  revoke(ruleId: number, txTime: number): boolean {
    assertNonNegativeInteger(txTime, 'txTime');
    this.assertMonotonicTxTime(txTime);

    let closed = false;
    for (const seg of this.segments) {
      if (seg.ruleId === ruleId && seg.txTo === FOREVER) {
        seg.txTo = txTime;
        closed = true;
      }
    }
    if (closed) this.lastTxTime = txTime;
    return closed;
  }

  /**
   * 双时态查询：在“系统于 txTime 时所知”的视角下，业务时刻 validTime 的价格（分）。
   * 无匹配规则时返回 undefined。
   */
  priceAt(validTime: number, txTime: number): number | undefined {
    const seg = this.segments.find(
      (s) =>
        s.validFrom <= validTime &&
        validTime < s.validTo &&
        s.txFrom <= txTime &&
        txTime < s.txTo,
    );
    return seg?.priceCents;
  }

  /** 返回全部历史切片的快照（只读副本），用于审计与调试。 */
  history(): PriceSegment[] {
    return this.segments.map((s) => ({ ...s }));
  }

  private assertMonotonicTxTime(txTime: number): void {
    if (this.lastTxTime !== null && txTime < this.lastTxTime) {
      throw new RangeError(`知悉时间不可回退：上一次为 ${this.lastTxTime}，收到 ${txTime}`);
    }
  }
}
