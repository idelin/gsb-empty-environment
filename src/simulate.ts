import { Barrier, ParticipantId, SettlementResult } from "./barrier";

const t0 = Date.now();
function log(msg: string): void {
  const t = String(Date.now() - t0).padStart(4, " ");
  console.log(`[t=${t}ms] ${msg}`);
}

function delay(ms: number): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

const SIZE = 4;
const TIMEOUT_MS = 100;

// ---- 可脚本化的参与者结算行为 --------------------------------------------
type Behavior =
  | { kind: "ok"; latencyMs: number }
  | { kind: "fail"; latencyMs: number }
  | { kind: "hang-then-ok"; latencyMs: number }; // 超过超时时间后才成功 => 迟到成功

const script = new Map<string, Behavior>();
function program(round: number, id: ParticipantId, b: Behavior): void {
  script.set(`${round}:${id}`, b);
}

// 第 1 轮：领导者 0 挂起 250ms 后迟到成功；1 号 20ms 内成功接任。
program(1, 0, { kind: "hang-then-ok", latencyMs: 250 });
program(1, 1, { kind: "ok", latencyMs: 20 });
program(1, 2, { kind: "ok", latencyMs: 10 });
program(1, 3, { kind: "ok", latencyMs: 10 });

// 第 2 轮：领导者正常快速成功（验证无接任、无重复广播）。
program(2, 0, { kind: "ok", latencyMs: 10 });

// 第 3 轮：0、1 失败，2 成功（验证依次接任、每人最多一次）。
program(3, 0, { kind: "fail", latencyMs: 10 });
program(3, 1, { kind: "fail", latencyMs: 10 });
program(3, 2, { kind: "ok", latencyMs: 10 });

// 第 4 轮：全员失败（验证无结果、无广播）。
program(4, 0, { kind: "fail", latencyMs: 5 });
program(4, 1, { kind: "fail", latencyMs: 5 });
program(4, 2, { kind: "fail", latencyMs: 5 });
program(4, 3, { kind: "fail", latencyMs: 5 });

async function settle(round: number, self: ParticipantId): Promise<string> {
  const b = script.get(`${round}:${self}`) || { kind: "ok", latencyMs: 10 } as Behavior;
  log(`round ${round}: participant ${self} starts settling (${b.kind}, ${b.latencyMs}ms)`);
  await delay(b.latencyMs);
  if (b.kind === "fail") {
    log(`round ${round}: participant ${self} settlement FAILED`);
    throw new Error("settlement failed");
  }
  const tag = b.kind === "hang-then-ok" ? "LATE success" : "success";
  log(`round ${round}: participant ${self} settlement ${tag}`);
  return `value-by-${self}-in-round-${round}`;
}

// ---- 广播与统计 -----------------------------------------------------------
const broadcasts = new Map<number, SettlementResult[]>();
function broadcast(result: SettlementResult): void {
  const list = broadcasts.get(result.round) || [];
  list.push(result);
  broadcasts.set(result.round, list);
  log(
    `round ${result.round}: >>> BROADCAST result by participant ${result.settler} ` +
      `(attempt #${result.attempt}, value=${result.value}) <<<`
  );
}

async function main(): Promise<void> {
  const barrier = new Barrier(SIZE, TIMEOUT_MS, settle, broadcast);

  for (const round of [1, 2, 3, 4]) {
    log(`round ${round}: participants arriving`);
    for (let id = 0; id < SIZE; id++) {
      barrier.arrive(id, round);
    }
    await barrier.waitRound(round);
    // 第 1 轮需要等领导者的迟到成功返回，验证它被丢弃
    if (round === 1) await delay(300);
    const committed = barrier.resultOf(round);
    log(
      `round ${round}: final result = ` +
        (committed ? `settler=${committed.settler} value=${committed.value}` : "(none, all failed)")
    );
  }

  // ---- 断言 ---------------------------------------------------------------
  let failures = 0;
  function check(cond: boolean, msg: string): void {
    if (!cond) {
      failures++;
      console.error(`ASSERTION FAILED: ${msg}`);
    }
  }

  for (const round of [1, 2, 3, 4]) {
    const n = (broadcasts.get(round) || []).length;
    check(n <= 1, `round ${round} broadcast ${n} times (expected <= 1)`);
  }
  const r1 = barrier.resultOf(1);
  check(r1 !== null && r1.settler === 1, "round 1 result must belong to participant 1 (leader timed out)");
  check((broadcasts.get(1) || []).length === 1, "round 1 must broadcast exactly once despite leader's late success");
  const r2 = barrier.resultOf(2);
  check(r2 !== null && r2.settler === 0 && r2.attempt === 1, "round 2 must be settled by leader on first attempt");
  const r3 = barrier.resultOf(3);
  check(r3 !== null && r3.settler === 2 && r3.attempt === 3, "round 3 must be settled by participant 2 after two failures");
  check(barrier.resultOf(4) === null, "round 4 must have no result (all failed)");
  check((broadcasts.get(4) || []).length === 0, "round 4 must not broadcast");

  if (failures === 0) {
    log("ALL ASSERTIONS PASSED");
  } else {
    process.exitCode = 1;
  }
}

main().catch((e) => {
  console.error(e);
  process.exitCode = 1;
});
