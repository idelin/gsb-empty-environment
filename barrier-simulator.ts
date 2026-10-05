/**
 * 屏障模拟器（Barrier Simulator）
 *
 * 规则：
 * 1. 每轮所有参与者到齐后屏障解除，由最小编号参与者首先结算；
 * 2. 结算失败或超时（默认 150ms）时，按编号升序依次接任，每人每轮最多结算一次；
 * 3. 轮次隔离：每轮使用全新的 RoundState 与独立的 fencing token，
 *    上一轮迟到的结果只能落在旧轮次的状态上，无权进入新一轮；
 * 4. 恰好一次广播：每轮只有第一个“持有效 fence 的成功结算”会被提交并广播，
 *    之后到达的成功结果（包括超时领导者的迟到成功）一律丢弃。
 *
 * 核心机制 —— fencing token（栅栏令牌）：
 *   每次发起结算尝试时 fence 递增；一旦某次尝试超时/失败，fence 立即再次递增作废。
 *   迟到的成功提交时携带的是旧 fence，与当前 fence 不匹配即被拒绝。
 *   因此“哪次结算有权成为该轮结果”的答案是：
 *   在提交瞬间仍持有该轮当前 fence、且第一个完成提交的那次结算。
 *   超时的领导者即便随后成功，也已失去该轮的结算权。
 */

type ParticipantId = number;

interface RoundResult {
  round: number;
  settler: ParticipantId;
  fence: number;
  value: string;
}

type SettleFn = (round: number, self: ParticipantId) => Promise<string>;

interface RoundState {
  readonly round: number;
  readonly order: readonly ParticipantId[];
  readonly arrivals: Set<ParticipantId>;
  readonly attempted: Set<ParticipantId>;
  cursor: number;
  fence: number;
  settled: boolean;
  result: RoundResult | null;
}

const sleep = (ms: number): Promise<void> =>
  new Promise((resolve) => setTimeout(resolve, ms));

class BarrierSimulator {
  private readonly participants: readonly ParticipantId[];
  private readonly timeoutMs: number;
  private readonly behaviors = new Map<ParticipantId, SettleFn>();
  private readonly rounds = new Map<number, RoundState>();
  private readonly broadcasts: RoundResult[] = [];

  constructor(participants: ParticipantId[], timeoutMs: number) {
    this.participants = [...participants].sort((a, b) => a - b);
    this.timeoutMs = timeoutMs;
  }

  setBehavior(id: ParticipantId, fn: SettleFn): void {
    this.behaviors.set(id, fn);
  }

  getBroadcasts(): readonly RoundResult[] {
    return this.broadcasts;
  }

  private log(message: string): void {
    console.log(message);
  }

  private async arrive(state: RoundState, id: ParticipantId): Promise<void> {
    await sleep(Math.floor(Math.random() * 30));
    state.arrivals.add(id);
    this.log(
      `[轮次 ${state.round}] P${id} 到达屏障 (${state.arrivals.size}/${state.order.length})`,
    );
  }

  async runRound(round: number): Promise<RoundResult | null> {
    // 轮次隔离：每轮创建全新状态，arrivals / attempted / fence 全部重置，
    // 上一轮的迟到提交只能命中旧 state 对象，永远不会进入这里。
    const state: RoundState = {
      round,
      order: this.participants,
      arrivals: new Set(),
      attempted: new Set(),
      cursor: 0,
      fence: 0,
      settled: false,
      result: null,
    };
    this.rounds.set(round, state);

    await Promise.all(this.participants.map((id) => this.arrive(state, id)));
    this.log(
      `[轮次 ${round}] 全部到齐，屏障解除，由 P${state.order[0]} 首先结算`,
    );

    while (!state.settled && state.cursor < state.order.length) {
      await this.attemptOnce(state);
    }

    if (!state.settled) {
      this.log(`[轮次 ${round}] 所有参与者均失败，本轮无结果`);
    }
    return state.result;
  }

  private async attemptOnce(state: RoundState): Promise<void> {
    const id = state.order[state.cursor];
    if (state.attempted.has(id)) {
      throw new Error(`P${id} 在轮次 ${state.round} 已结算过，违反“每人最多一次”`);
    }
    state.attempted.add(id);
    state.fence += 1;
    const fence = state.fence;
    this.log(`[轮次 ${state.round}] P${id} 开始结算 (fence=${fence})`);

    const settle = this.behaviors.get(id)!(state.round, id);

    // 无论是否超时，真正的结算 Promise 完成时都会尝试提交；
    // 是否被接受由 tryCommit 中的 settled / fence 检查决定。
    settle.then(
      (value) => this.tryCommit(state, fence, id, value),
      (err: unknown) =>
        this.log(`[轮次 ${state.round}] P${id} 结算失败: ${String(err)}`),
    );

    const outcome = await Promise.race([
      settle.then(
        () => "completed" as const,
        () => "failed" as const,
      ),
      sleep(this.timeoutMs).then(() => "timeout" as const),
    ]);

    if (outcome === "completed") {
      return; // 成功，tryCommit 已经提交并广播
    }

    // 失败或超时：先作废旧 fence（关键），再移交下一位。
    // 此后该尝试即使迟到成功，也会因 fence 不匹配被丢弃。
    state.fence += 1;
    state.cursor += 1;
    if (outcome === "timeout") {
      this.log(
        `[轮次 ${state.round}] P${id} 超时（>${this.timeoutMs}ms），视为失败，fence 作废，移交下一位`,
      );
    }
  }

  private tryCommit(
    state: RoundState,
    fence: number,
    settler: ParticipantId,
    value: string,
  ): void {
    if (state.settled) {
      this.log(
        `[轮次 ${state.round}] 丢弃 P${settler} 的迟到/重复成功：本轮已由 P${state.result!.settler} 广播`,
      );
      return;
    }
    if (fence !== state.fence) {
      this.log(
        `[轮次 ${state.round}] 丢弃 P${settler} 的过期成功：fence ${fence} 已失效（当前 fence ${state.fence}）`,
      );
      return;
    }
    state.settled = true;
    state.result = { round: state.round, settler, fence, value };
    this.broadcast(state.result);
  }

  private broadcast(result: RoundResult): void {
    // 恰好一次不变式：同一轮绝不允许第二次广播
    if (this.broadcasts.some((r) => r.round === result.round)) {
      throw new Error(`轮次 ${result.round} 重复广播，违反恰好一次语义`);
    }
    this.broadcasts.push(result);
    this.log(
      `[轮次 ${result.round}] ✅ 广播结果：P${result.settler} 的结算生效 -> ${result.value}`,
    );
  }
}

function assert(condition: boolean, message: string): void {
  if (!condition) {
    throw new Error(`断言失败: ${message}`);
  }
}

async function main(): Promise<void> {
  const TIMEOUT_MS = 150;
  const sim = new BarrierSimulator([1, 2, 3], TIMEOUT_MS);

  // P1：第 1 轮“超时但迟到成功”（160ms 才完成，恰好落在“已超时、接任者
  // 尚未提交”的窗口内，将被 fence 检查拦截）；第 2、4 轮正常；第 3 轮快速失败
  sim.setBehavior(1, async (round) => {
    if (round === 1) {
      await sleep(160);
      return "P1 的迟到成功";
    }
    if (round === 3) {
      await sleep(30);
      throw new Error("P1 主动失败");
    }
    await sleep(20);
    return "P1 的及时成功";
  });

  // P2：第 3 轮“超时但迟到成功”（400ms 才完成，落地时第 4 轮已开始，
  // 用于验证轮次隔离），其余轮次快速成功
  sim.setBehavior(2, async (round) => {
    if (round === 3) {
      await sleep(400);
      return "P2 的迟到成功";
    }
    await sleep(50);
    return "P2 的及时成功";
  });

  // P3：始终快速成功
  sim.setBehavior(3, async () => {
    await sleep(40);
    return "P3 的及时成功";
  });

  // 第 1 轮：P1 超时 -> P2 接任成功；P1 的迟到成功被 fence 检查丢弃
  const r1 = await sim.runRound(1);
  // 第 2 轮：P1 重新获得结算权（“每人最多一次”按轮重置），及时成功
  const r2 = await sim.runRound(2);
  // 第 3 轮：P1 失败 -> P2 超时（迟到成功被丢弃）-> P3 接任成功
  const r3 = await sim.runRound(3);
  // 第 4 轮紧接着开始：P2 在第 3 轮的迟到成功（400ms）会落在本轮进行期间，
  // 它只能命中第 3 轮的旧状态，绝不会污染第 4 轮 —— 轮次隔离的运行时验证
  const r4 = await sim.runRound(4);

  // 等待所有迟到的 Promise 落地，让丢弃日志全部打印出来
  await sleep(600);

  const broadcasts = sim.getBroadcasts();

  assert(broadcasts.length === 4, "每轮必须恰好广播一次");
  assert(
    broadcasts.map((b) => b.round).join(",") === "1,2,3,4",
    "广播必须按轮次各出现一次",
  );
  assert(
    r1 !== null && broadcasts[0].settler === 2 && r1.value === "P2 的及时成功",
    "第 1 轮结果必须属于接任者 P2，而非迟到成功的领导者 P1",
  );
  assert(
    r2 !== null && broadcasts[1].settler === 1 && r2.value === "P1 的及时成功",
    "第 2 轮 P1 应重新获得结算权（“每人最多一次”按轮重置）",
  );
  assert(
    r3 !== null && broadcasts[2].settler === 3 && r3.value === "P3 的及时成功",
    "第 3 轮应沿编号一路接任到 P3",
  );
  assert(
    r4 !== null && broadcasts[3].settler === 1 && r4.value === "P1 的及时成功",
    "第 4 轮不受第 3 轮迟到成功影响，由 P1 正常结算",
  );

  console.log("\n全部断言通过 ✅");
  console.log(
    "结论：每轮只有‘提交瞬间仍持有当前 fence 且最先完成提交’的结算有权成为该轮结果；",
  );
  console.log(
    "      超时领导者的迟到成功因 fence 已作废（且本轮已广播）而被丢弃，不参与结果。",
  );
}

main().catch((err: unknown) => {
  console.error(err);
});
