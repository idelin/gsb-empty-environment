import { Network } from './network.ts';
import { Process, type SnapshotRecord } from './process.ts';
import type { TransferMessage } from './messages.ts';

export interface ClusterOptions {
  balances: number[];
  /** 每次转账前的随机延迟 (ms)。 */
  tickDelay?: () => number;
  /** 网络投递延迟 (ms)。 */
  netDelay?: () => number;
  log?: (line: string) => void;
}

export interface SnapshotReport {
  snapshotId: string;
  /** 各进程记录的本地状态（余额）。 */
  localStates: Record<string, number>;
  /** 各有向信道记录的在途转账消息。 */
  channelStates: Record<string, TransferMessage[]>;
  /** 本地状态之和。 */
  localTotal: number;
  /** 在途金额之和。 */
  inTransitTotal: number;
  /** 快照总资产 = localTotal + inTransitTotal。 */
  grandTotal: number;
}

const sleep = (ms: number) => new Promise<void>((r) => setTimeout(r, ms));

/**
 * 全连接进程集群：持续进行并发转账，并支持在业务不暂停的情况下
 * 发起 Chandy-Lamport 全局一致性快照。
 */
export class Cluster {
  readonly processes: Process[] = [];
  readonly network: Network;
  private readonly log: (line: string) => void;
  private running = false;
  private transferCount = 0;
  private readonly options: ClusterOptions;

  constructor(options: ClusterOptions) {
    this.options = options;
    this.log = options.log ?? (() => {});
    const ids = options.balances.map((_, i) => String.fromCharCode(65 + i));
    const byId = new Map<string, Process>();
    this.network = new Network(
      (msg) => byId.get(msg.to)!.onMessage(msg),
      options.netDelay ?? (() => 0),
    );
    for (let i = 0; i < ids.length; i++) {
      const peers = ids.filter((id) => id !== ids[i]);
      const p = new Process(ids[i], options.balances[i], peers, this.network, this.log);
      byId.set(ids[i], p);
      this.processes.push(p);
    }
  }

  get initialTotal(): number {
    return this.options.balances.reduce((a, b) => a + b, 0);
  }

  /** 当前全网真实总资产 = 各进程余额 + 网络在途转账金额（应恒等于 initialTotal）。 */
  get liveTotal(): number {
    let total = this.processes.reduce((sum, p) => sum + p.balance, 0);
    for (const msg of this.network.inFlight) total += msg.amount;
    return total;
  }

  /** 启动后台转账流量：每个进程随机向其他节点转账，直到 stopTraffic()。 */
  startTraffic(): void {
    this.running = true;
    const tickDelay = this.options.tickDelay ?? (() => 1);
    for (const p of this.processes) {
      void (async () => {
        while (this.running) {
          await sleep(tickDelay());
          const peers = this.processes.filter((q) => q !== p);
          const to = peers[Math.floor(Math.random() * peers.length)];
          const amount = 1 + Math.floor(Math.random() * 10);
          if (p.sendTransfer(to.id, amount)) this.transferCount++;
        }
      })();
    }
  }

  stopTraffic(): void {
    this.running = false;
  }

  get transfersSent(): number {
    return this.transferCount;
  }

  /** 由指定进程发起快照，并等待全网所有进程完成记录。 */
  async takeSnapshot(initiatorId: string, snapshotId: string): Promise<SnapshotReport> {
    const initiator = this.processes.find((p) => p.id === initiatorId);
    if (!initiator) throw new Error(`unknown process ${initiatorId}`);
    initiator.initiateSnapshot(snapshotId);
    while (!this.processes.every((p) => p.isSnapshotComplete(snapshotId))) {
      await sleep(1);
    }
    return this.buildReport(snapshotId);
  }

  private buildReport(snapshotId: string): SnapshotReport {
    const localStates: Record<string, number> = {};
    const channelStates: Record<string, TransferMessage[]> = {};
    let localTotal = 0;
    let inTransitTotal = 0;
    for (const p of this.processes) {
      const snap = p.getSnapshot(snapshotId) as SnapshotRecord;
      localStates[p.id] = snap.balance;
      localTotal += snap.balance;
      for (const ch of snap.channels.values()) {
        channelStates[`${ch.from}->${p.id}`] = [...ch.messages];
        for (const m of ch.messages) inTransitTotal += m.amount;
      }
    }
    return {
      snapshotId,
      localStates,
      channelStates,
      localTotal,
      inTransitTotal,
      grandTotal: localTotal + inTransitTotal,
    };
  }
}
