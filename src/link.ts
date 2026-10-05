import type { Sim } from "./sim.ts";
import type { Packet } from "./packets.ts";

// 单向链路：固定时延 + 均匀抖动（产生乱序）+ 随机丢包。
// up=false 表示链路已关闭（迁移排空期结束）；已在途的包仍会送达。
export class Link {
  up = true;
  dropped = 0;
  readonly id: number;
  readonly latency: number;
  readonly jitter: number;
  readonly lossRate: number;

  constructor(id: number, latency: number, jitter: number, lossRate: number) {
    this.id = id;
    this.latency = latency;
    this.jitter = jitter;
    this.lossRate = lossRate;
  }

  transmit(pkt: Packet, sim: Sim, rng: () => number, deliver: (p: Packet) => void): void {
    if (!this.up || rng() < this.lossRate) {
      this.dropped++;
      return;
    }
    const d = this.latency + (this.jitter > 0 ? rng() * this.jitter : 0);
    sim.schedule(d, () => deliver(pkt));
  }
}
