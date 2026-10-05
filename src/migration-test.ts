import { Sim } from "./sim.ts";
import { Endpoint } from "./endpoint.ts";
import { Link } from "./link.ts";
import { mulberry32 } from "./prng.ts";
import { assert } from "./assert.ts";

// 定向测试：精确验证迁移点与确认合并规则。
//   旧链路迟到确认可推进已发送前缀，但不能确认新链路才发的数据；
//   任何 ACK 都不能确认尚未发送的数据。
export function directedMigrationTest(): void {
  const sim = new Sim();
  const rng = mulberry32(42);
  const opts = { totalMessages: 18, maxSendBuffer: 10, maxRecvWindow: 16, ackDelay: 25, retxBatch: 8 };
  const a = new Endpoint(sim, rng, { name: "A", ...opts });
  const b = new Endpoint(sim, rng, { name: "B", ...opts, totalMessages: 0 });
  for (const ep of [a, b]) {
    ep.attach(0, new Link(0, 0, 0, 0));
    ep.attach(1, new Link(1, 0, 0, 0));
  }
  a.connect(b);
  b.connect(a);

  a.start(); // 0..9 全部在旧路径 0 上发出（发送窗口=10）
  assert(a.unackedCount() === 10, "10 messages in flight on old path");

  a.migrateTo(1); // 迁移点：此后新数据走新路径

  // 旧链路迟到 ACK：推进已发送前缀到 7，窗口释放后 10..17 在新路径发出
  a.onPacket({ kind: "ack", pathId: 0, cumAck: 7, sack: [], echoSeq: 7, echoSentAt: 0 });
  assert(a.ackedPrefix === 7, "late old-path ack advances sent prefix to 7");
  assert(a.unackedCount() === 10, "window refilled with new-path data");

  // 旧链路迟到 ACK 声称 cumAck=15：被钳制到旧路径已发前沿 9，
  // 可推进前缀（8、9 被确认），但新链路的 10..15 不能被它确认。
  a.onPacket({ kind: "ack", pathId: 0, cumAck: 15, sack: [], echoSeq: 7, echoSentAt: 0 });
  assert(a.ackedPrefix === 9, "old-path ack clamped to old-path sent frontier (9)");
  assert(a.unackedCount() === 8, "10..17 remain unacked");

  // 新链路 ACK 正常合并到 15。
  a.onPacket({ kind: "ack", pathId: 1, cumAck: 15, sack: [], echoSeq: 15, echoSentAt: 0 });
  assert(a.ackedPrefix === 15, "new-path ack merges up to 15");

  // 越界 ACK（声称 99，实际只发到 17）：钳制到 17，绝不确认未发送数据。
  a.onPacket({ kind: "ack", pathId: 1, cumAck: 99, sack: [], echoSeq: 15, echoSentAt: 0 });
  assert(a.ackedPrefix === 17, "ack beyond sent frontier clamped to 17");
  assert(a.done(), "all 18 messages confirmed");
  assert(a.stats.clampedAcks === 2, "exactly two acks were clamped");
  assert(a.stats.lateAcksAfterMigration >= 1, "late old-path acks observed");
  assert(a.stats.oldPathAckAdvanced >= 1, "old-path ack advanced the prefix");

  console.log("[directed] migration point & ack-merge rules OK " +
    `(clamped=${a.stats.clampedAcks}, lateOldPathAcks=${a.stats.lateAcksAfterMigration})`);
}
