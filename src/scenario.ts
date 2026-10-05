import { Sim } from "./sim.ts";
import { Endpoint } from "./endpoint.ts";
import { Link } from "./link.ts";
import { mulberry32 } from "./prng.ts";
import { assert } from "./assert.ts";

export interface LinkParams {
  latency: number;
  jitter: number;
  lossRate: number;
}

export interface ScenarioParams {
  seed: number;
  messages: number;
  aMigrateAt: number; // A 的迁移时刻（虚拟毫秒）
  bMigrateAt: number; // B 的迁移时刻
  drainMs: number; // 迁移后旧链路保持可用的排空期
  ackDelay: number;
  path0: LinkParams;
  path1: LinkParams;
  tMax: number;
}

export interface ScenarioResult {
  seed: number;
  finishTime: number;
  a: Endpoint["stats"];
  b: Endpoint["stats"];
}

const MAX_SEND = 32; // 发送缓冲上界
const MAX_RECV = 64; // 接收乱序缓存上界

// 双向流场景：A、B 各向对方发送 messages 条逻辑消息，期间先后迁移。
export function runScenario(p: ScenarioParams): ScenarioResult {
  const sim = new Sim();
  const rng = mulberry32(p.seed);
  const mk = (name: string): Endpoint =>
    new Endpoint(sim, rng, {
      name,
      totalMessages: p.messages,
      maxSendBuffer: MAX_SEND,
      maxRecvWindow: MAX_RECV,
      ackDelay: p.ackDelay,
      retxBatch: 8,
    });
  const a = mk("A");
  const b = mk("B");

  // 路径 0 = 旧链路，路径 1 = 新链路；两个方向各自一条单向链路。
  const ab0 = new Link(0, p.path0.latency, p.path0.jitter, p.path0.lossRate);
  const ab1 = new Link(1, p.path1.latency, p.path1.jitter, p.path1.lossRate);
  const ba0 = new Link(0, p.path0.latency, p.path0.jitter, p.path0.lossRate);
  const ba1 = new Link(1, p.path1.latency, p.path1.jitter, p.path1.lossRate);
  a.attach(0, ab0);
  a.attach(1, ab1);
  b.attach(0, ba0);
  b.attach(1, ba1);
  a.connect(b);
  b.connect(a);

  sim.schedule(p.aMigrateAt, () => a.migrateTo(1));
  sim.schedule(p.bMigrateAt, () => b.migrateTo(1));
  const closeAt = Math.max(p.aMigrateAt, p.bMigrateAt) + p.drainMs;
  sim.schedule(closeAt, () => {
    ab0.up = false; // 排空期结束，旧链路关闭（在途包仍会送达）
    ba0.up = false;
  });

  a.start();
  b.start();
  const ok = sim.runUntil(() => a.done() && b.done(), p.tMax);
  assert(ok, `scenario seed=${p.seed} did not converge within tMax=${p.tMax}`);

  // 恰好一次：交付序列必须严格等于 0..N-1（无丢失、无重复、无乱序）。
  verifyStream(a, p.messages);
  verifyStream(b, p.messages);
  // 有界内存：高水位不得超过配置上界。
  assert(a.stats.highWaterSend <= MAX_SEND && b.stats.highWaterSend <= MAX_SEND, "send buffer bound violated");
  assert(a.stats.highWaterRecv <= MAX_RECV && b.stats.highWaterRecv <= MAX_RECV, "recv window bound violated");
  // 两边都发生了重传。
  assert(a.stats.retx > 0 && b.stats.retx > 0, "expected retransmissions on both sides");

  return { seed: p.seed, finishTime: sim.now, a: a.stats, b: b.stats };
}

function verifyStream(ep: Endpoint, n: number): void {
  assert(
    ep.delivered.length === n,
    `${ep.name}: expected ${n} delivered messages, got ${ep.delivered.length}`,
  );
  for (let i = 0; i < n; i++) {
    assert(ep.delivered[i] === i, `${ep.name}: delivery violation at index ${i} (got ${ep.delivered[i]})`);
  }
}
