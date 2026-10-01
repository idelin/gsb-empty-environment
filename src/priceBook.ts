/**
 * 双时态价格规则引擎（Bitemporal Price Book）
 *
 * 两个时间维度：
 *  - Valid Time（业务生效时间）：价格在业务上何时生效，区间左闭右开 [validFrom, validTo)
 *  - Transaction Time（系统知悉时间）：系统何时知悉该规则，区间左闭右开 [txFrom, txTo)
 *
 * 语义：
 *  - 查询 priceAt(validAt, txAt) 表示"在知悉时间 txAt 时，系统认为业务时间 validAt 的价格"
 *  - 多条规则重叠时，按知悉时间后入为主（txFrom 最大者胜出）
 *  - 撤销（retract）只是把该规则的知悉区间在撤销时刻封闭，
 *    因此只影响撤销发生之后（txAt >= 撤销时刻）的查询，历史知悉时间查询不受影响
 *  - 金额一律使用整数分（number, 整数）
 */

export interface PriceRuleInput {
  /** 价格，整数分 */
  priceCents: number;
  /** 业务生效起点（含） */
  validFrom: number;
  /** 业务生效终点（不含） */
  validTo: number;
  /** 系统知悉时刻（含） */
  txFrom: number;
}

export interface PriceRule extends PriceRuleInput {
  id: number;
  /** 知悉终点（不含），Infinity 表示当前仍有效 */
  txTo: number;
}

export class PriceBook {
  private rules: PriceRule[] = [];
  private nextId = 1;

  /** 登记一条价格规则，返回规则 id */
  addRule(input: PriceRuleInput): number {
    const { priceCents, validFrom, validTo, txFrom } = input;
    if (!Number.isInteger(priceCents)) {
      throw new Error("priceCents 必须是整数分");
    }
    if (!(validFrom < validTo)) {
      throw new Error("业务区间必须满足 validFrom < validTo（左闭右开）");
    }
    const rule: PriceRule = { id: this.nextId++, txTo: Infinity, ...input };
    this.rules.push(rule);
    return rule.id;
  }

  /**
   * 撤销一条规则：在 txAt 时刻封闭其知悉区间。
   * 仅影响 txAt 及之后的知悉时间查询；txAt 之前的查询仍能看到该规则。
   */
  retract(id: number, txAt: number): void {
    const rule = this.rules.find((r) => r.id === id && r.txTo === Infinity);
    if (!rule) {
      throw new Error(`规则 ${id} 不存在或已被撤销`);
    }
    if (txAt < rule.txFrom) {
      throw new Error("撤销时刻不能早于规则的知悉时刻");
    }
    rule.txTo = txAt;
  }

  /**
   * 查询：在知悉时间 txAt 时，业务时间 validAt 的价格（整数分）。
   * 无匹配规则时返回 null。
   */
  priceAt(validAt: number, txAt: number): number | null {
    let best: PriceRule | null = null;
    for (const rule of this.rules) {
      const inValid = rule.validFrom <= validAt && validAt < rule.validTo;
      const inTx = rule.txFrom <= txAt && txAt < rule.txTo;
      if (!inValid || !inTx) continue;
      // 后知悉者优先；知悉时刻相同则 id 大者（后登记）优先
      if (
        best === null ||
        rule.txFrom > best.txFrom ||
        (rule.txFrom === best.txFrom && rule.id > best.id)
      ) {
        best = rule;
      }
    }
    return best === null ? null : best.priceCents;
  }

  /** 只读快照，便于调试与测试 */
  snapshot(): readonly PriceRule[] {
    return this.rules.map((r) => ({ ...r }));
  }
}
