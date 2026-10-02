/**
 * 运行样例：模拟网络抖动导致 M1 先于其因果前驱 M0 到达接收端。
 *
 * 场景：
 * - 节点 A 连续广播 M0、M1（M1 因果依赖 M0）。
 * - 抖动网络给 M0 施加 300ms 延迟，给 M1 只施加 20ms 延迟，
 *   于是接收端 B 先收到 M1。
 * - 中间件检测到 M0 未交付，将 M1 暂存隔离队列；
 *   M0 到达并交付后，自动级联交付 M1 —— 应用层永远看不到因果倒置。
 */

import { CausalBroadcast, type Message } from "./causalBroadcast.ts";
import { format } from "./vectorClock.ts";

/** 带抖动（可指定逐条延迟）的模拟网络 */
class JitteryNetwork {
  private delays = new Map<string, number>();
  private receivers: CausalBroadcast[];

  constructor(receivers: CausalBroadcast[]) {
    this.receivers = receivers;
  }

  /** 为特定消息指定传输延迟（毫秒），未指定的消息使用随机抖动。 */
  setDelay(messageId: string, ms: number): void {
    this.delays.set(messageId, ms);
  }

  send(message: Message): void {
    const ms = this.delays.get(message.id) ?? Math.floor(Math.random() * 50);
    setTimeout(() => {
      for (const receiver of this.receivers) {
        receiver.receive(message);
      }
    }, ms);
  }
}

const nodeA = new CausalBroadcast("A");
const nodeB = new CausalBroadcast("B");

const network = new JitteryNetwork([nodeB]);
nodeA.setTransport((m) => network.send(m));

const deliveredAtB: string[] = [];
nodeB.onDeliver((m) => {
  deliveredAtB.push(m.id);
  console.log(
    `  [B 应用层] 交付 ${m.id}  内容=${JSON.stringify(m.payload)}  时钟=${format(m.clock)}`,
  );
});

// 人为制造乱序：M1 跑得快（20ms），M0 被网络卡住（300ms）
network.setDelay("A#1", 300);
network.setDelay("A#2", 20);

console.log("== 节点 A 连续广播 M0、M1（M1 因果依赖 M0）==");
nodeA.broadcast("M0: 第一条消息");
nodeA.broadcast("M1: 依赖 M0 的回复");

setTimeout(() => {
  console.log(`\n== 50ms 时：M1 已先到，但被隔离缓存 ==`);
  console.log(`  B 隔离队列长度 = ${nodeB.bufferedCount}（M1 等待 M0）`);
  console.log(`  B 已交付序列 = [${deliveredAtB.join(", ")}]（为空，无因果倒置）`);
}, 50);

setTimeout(() => {
  console.log(`\n== 350ms 时：M0 到达，触发级联交付 ==`);
  console.log(`  B 隔离队列长度 = ${nodeB.bufferedCount}`);
  console.log(`  B 最终交付序列 = [${deliveredAtB.join(", ")}]`);
  const ok =
    deliveredAtB.join(",") === "A#1,A#2" && nodeB.bufferedCount === 0;
  console.log(ok ? "\n✔ 因果顺序保持：M0 先于 M1 交付，无因果倒置" : "\n✘ 验证失败");
  process.exit(ok ? 0 : 1);
}, 350);
