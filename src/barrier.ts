/**
 * 屏障模拟器核心。
 *
 * 规则：
 * 1. 每轮所有参与者到齐后，由最小编号参与者首先结算。
 * 2. 结算失败（含超时）则按编号升序依次接任，每人每轮最多结算一次。
 * 3. 轮次隔离：每轮状态独立，任何成功结果在每轮内只能被接受并广播一次。
 * 4. 超时未返回的结算若稍后“迟到成功”，只有在该轮结果尚未被提交时才有权成为该轮结果；
 *    否则被丢弃，不重复广播。
 */

export type ParticipantId = number;

export interface SettlementResult {
  round: number;
  settler: ParticipantId;
  value: string;
  /** 结算链中第几次尝试（从 1 开始） */
  attempt: number;
}

/** 参与者实际的结算行为，由外部注入以便模拟成功 / 失败 / 超时 / 迟到成功。 */
export type SettleFn = (round: number, self: ParticipantId) => Promise<string>;

export type BroadcastFn = (result: SettlementResult) => void;

interface RoundState {
  readonly round: number;
  /** 已到达的参与者 */
  readonly arrived: Set<ParticipantId>;
  /** 本轮已被要求结算过的参与者（每人最多一次） */
  readonly attempted: Set<ParticipantId>;
  /** 本轮唯一被接受的结果；非空即表示该轮已关闭 */
  committed: SettlementResult | null;
  /** 结算链 Promise，保证到齐后只启动一次 */
  chain: Promise<void> | null;
}

type SettleOutcome =
  | { kind: "value"; value: string }
  | { kind: "error"; error: unknown }
  | { kind: "timeout" };

function raceTimeout(p: Promise<string>, ms: number): Promise<SettleOutcome> {
  return new Promise<SettleOutcome>((resolve) => {
    const timer = setTimeout(() => resolve({ kind: "timeout" }), ms);
    p.then(
      (value) => {
        clearTimeout(timer);
        resolve({ kind: "value", value });
      },
      (error) => {
        clearTimeout(timer);
        resolve({ kind: "error", error });
      }
    );
  });
}

export class Barrier {
  /** 每轮状态相互独立，实现轮次隔离 */
  private readonly rounds = new Map<number, RoundState>();

  constructor(
    private readonly size: number,
    private readonly settleTimeoutMs: number,
    private readonly settle: SettleFn,
    private readonly broadcast: BroadcastFn
  ) {
    if (size <= 0) throw new Error("size must be positive");
  }

  private stateOf(round: number): RoundState {
    let st = this.rounds.get(round);
    if (!st) {
      st = {
        round,
        arrived: new Set<ParticipantId>(),
        attempted: new Set<ParticipantId>(),
        committed: null,
        chain: null,
      };
      this.rounds.set(round, st);
    }
    return st;
  }

  /** 参与者到达屏障。到齐后自动启动结算链。 */
  arrive(id: ParticipantId, round: number): void {
    if (id < 0 || id >= this.size) {
      throw new Error(`participant ${id} out of range`);
    }
    const st = this.stateOf(round);
    if (st.arrived.has(id)) {
      throw new Error(`participant ${id} arrived twice in round ${round}`);
    }
    st.arrived.add(id);
    if (st.arrived.size === this.size && st.chain === null) {
      st.chain = this.runSettlementChain(st);
    }
  }

  /** 等待某轮结算链结束（结果可能已提交，也可能全部失败）。 */
  async waitRound(round: number): Promise<void> {
    const st = this.stateOf(round);
    if (st.chain !== null) await st.chain;
  }

  /** 查询某轮已被接受的结果（无则 null）。 */
  resultOf(round: number): SettlementResult | null {
    const st = this.rounds.get(round);
    return st ? st.committed : null;
  }

  /** 按编号升序依次尝试结算；任一成功后停止。 */
  private async runSettlementChain(st: RoundState): Promise<void> {
    for (let id = 0; id < this.size; id++) {
      if (st.committed !== null) return;
      if (st.attempted.has(id)) continue; // 每人最多一次
      st.attempted.add(id);
      const committed = await this.attemptSettle(st, id);
      if (committed) return;
    }
  }

  private async attemptSettle(st: RoundState, id: ParticipantId): Promise<boolean> {
    const settlePromise = this.settle(st.round, id);
    const outcome = await raceTimeout(settlePromise, this.settleTimeoutMs);
    switch (outcome.kind) {
      case "value":
        return this.tryCommit(st, id, outcome.value);
      case "error":
        return false;
      case "timeout":
        // 超时后立即接任；但迟到成功仍可能到来，届时走统一的 tryCommit 裁决。
        settlePromise.then(
          (value) => {
            this.tryCommit(st, id, value);
          },
          () => undefined
        );
        return false;
    }
  }

  /**
   * 唯一的提交点，也是“哪次结算有权成为该轮结果”的裁决点：
   * 第一个到达这里的成功结果获得该轮的所有权，之后的一切成功（包括
   * 领导者的迟到成功）都被丢弃，广播因此恰好发生一次。
   */
  private tryCommit(st: RoundState, settler: ParticipantId, value: string): boolean {
    if (st.committed !== null) return false; // 该轮已有归属，迟到者丢弃
    st.committed = {
      round: st.round,
      settler,
      value,
      attempt: st.attempted.size,
    };
    this.broadcast(st.committed); // 与提交绑定，保证每轮恰好广播一次
    return true;
  }
}
