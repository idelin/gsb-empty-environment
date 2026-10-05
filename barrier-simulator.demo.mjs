// barrier-simulator.demo.mjs
// barrier-simulator.ts 的去类型孪生文件，仅用于在无 tsc 的环境下用 Node 直接验证逻辑。
// 两者逻辑完全一致；正式交付物为 barrier-simulator.ts。

const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));

class BarrierSimulator {
  constructor(participants, timeoutMs) {
    this.participants = [...participants].sort((a, b) => a - b);
    this.timeoutMs = timeoutMs;
    this.behaviors = new Map();
    this.rounds = new Map();
    this.broadcasts = [];
  }

  setBehavior(id, fn) {
    this.behaviors.set(id, fn);
  }

  getBroadcasts() {
    return this.broadcasts;
  }

  log(message) {
    console.log(message);
  }

  async arrive(state, id) {
    await sleep(Math.floor(Math.random() * 30));
    state.arrivals.add(id);
    this.log(
      `[轮次 ${state.round}] P${id} 到达屏障 (${state.arrivals.size}/${state.order.length})`,
    );
  }

  async runRound(round) {
    const state = {
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

  async attemptOnce(state) {
    const id = state.order[state.cursor];
    if (state.attempted.has(id)) {
      throw new Error(`P${id} 在轮次 ${state.round} 已结算过，违反“每人最多一次”`);
    }
    state.attempted.add(id);
    state.fence += 1;
    const fence = state.fence;
    this.log(`[轮次 ${state.round}] P${id} 开始结算 (fence=${fence})`);

    const settle = this.behaviors.get(id)(state.round, id);

    settle.then(
      (value) => this.tryCommit(state, fence, id, value),
      (err) =>
        this.log(`[轮次 ${state.round}] P${id} 结算失败: ${String(err)}`),
    );

    const outcome = await Promise.race([
      settle.then(
        () => "completed",
        () => "failed",
      ),
      sleep(this.timeoutMs).then(() => "timeout"),
    ]);

    if (outcome === "completed") {
      return;
    }

    state.fence += 1;
    state.cursor += 1;
    if (outcome === "timeout") {
      this.log(
        `[轮次 ${state.round}] P${id} 超时（>${this.timeoutMs}ms），视为失败，fence 作废，移交下一位`,
      );
    }
  }

  tryCommit(state, fence, settler, value) {
    if (state.settled) {
      this.log(
        `[轮次 ${state.round}] 丢弃 P${settler} 的迟到/重复成功：本轮已由 P${state.result.settler} 广播`,
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

  broadcast(result) {
    if (this.broadcasts.some((r) => r.round === result.round)) {
      throw new Error(`轮次 ${result.round} 重复广播，违反恰好一次语义`);
    }
    this.broadcasts.push(result);
    this.log(
      `[轮次 ${result.round}] ✅ 广播结果：P${result.settler} 的结算生效 -> ${result.value}`,
    );
  }
}

function assert(condition, message) {
  if (!condition) {
    throw new Error(`断言失败: ${message}`);
  }
}

async function main() {
  const TIMEOUT_MS = 150;
  const sim = new BarrierSimulator([1, 2, 3], TIMEOUT_MS);

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

  sim.setBehavior(2, async (round) => {
    if (round === 3) {
      await sleep(400);
      return "P2 的迟到成功";
    }
    await sleep(50);
    return "P2 的及时成功";
  });

  sim.setBehavior(3, async () => {
    await sleep(40);
    return "P3 的及时成功";
  });

  const r1 = await sim.runRound(1);
  const r2 = await sim.runRound(2);
  const r3 = await sim.runRound(3);
  const r4 = await sim.runRound(4);

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

main().catch((err) => {
  console.error(err);
});
