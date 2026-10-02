import type { Message, TransferMessage } from './messages.ts';
import type { Network } from './network.ts';

/** 单条入信道的快照记录：Marker 到达前在途的转账消息集合。 */
export interface ChannelState {
  readonly from: string;
  recording: boolean;
  readonly messages: TransferMessage[];
}

/** 一个进程对某次快照的本地记录。 */
export interface SnapshotRecord {
  readonly snapshotId: string;
  /** 记录本地状态时刻的余额。 */
  readonly balance: number;
  /** 记录本地状态时刻的逻辑时钟（割点）。 */
  readonly cutClock: number;
  /** 按发送方索引的入信道状态。 */
  readonly channels: Map<string, ChannelState>;
}

/**
 * 分布式系统中的一个进程（节点）。
 * 实现 Chandy-Lamport 算法：
 *  - 发起者：记录本地状态 -> 向所有出信道发送 Marker -> 开始记录所有入信道；
 *  - 首次收到 Marker：记录本地状态 -> 向所有出信道转发 Marker -> 记录除该信道外的入信道；
 *  - 再次收到 Marker：停止记录对应入信道，已记录消息即为该信道在途状态。
 */
export class Process {
  balance: number;
  /** 本地逻辑时钟，每个本地事件（发送/接收/记录）递增。 */
  clock = 0;
  private readonly snapshots = new Map<string, SnapshotRecord>();
  private msgSeq = 0;
  private readonly peers: string[];
  private readonly network: Network;
  private readonly log: (line: string) => void;
  readonly id: string;

  constructor(
    id: string,
    initialBalance: number,
    peers: string[],
    network: Network,
    log: (line: string) => void = () => {},
  ) {
    this.id = id;
    this.balance = initialBalance;
    this.peers = peers;
    this.network = network;
    this.log = log;
  }

  /** 发起一次全局快照（通常由某一个节点触发，无需暂停业务）。 */
  initiateSnapshot(snapshotId: string): void {
    this.log(`[${this.id}] 发起快照 ${snapshotId}`);
    this.recordLocalState(snapshotId);
    this.sendMarkers(snapshotId);
  }

  /** 发送一笔转账：发送方立即扣款，接收方投递后入账。余额不足时返回 false。 */
  sendTransfer(to: string, amount: number): boolean {
    if (amount <= 0 || amount > this.balance) return false;
    this.clock++;
    this.balance -= amount;
    this.network.send({
      kind: 'transfer',
      id: ++this.msgSeq,
      from: this.id,
      to,
      amount,
      senderClock: this.clock,
    });
    return true;
  }

  /** 网络投递入口。 */
  onMessage(msg: Message): void {
    this.clock++;
    if (msg.kind === 'transfer') this.onTransfer(msg);
    else this.onMarker(msg.snapshotId, msg.from);
  }

  private onTransfer(msg: TransferMessage): void {
    // 先入账，再把消息追加到所有正在记录该信道的快照中。
    this.balance += msg.amount;
    for (const snap of this.snapshots.values()) {
      const ch = snap.channels.get(msg.from);
      if (ch?.recording) ch.messages.push(msg);
    }
  }

  private onMarker(snapshotId: string, from: string): void {
    let snap = this.snapshots.get(snapshotId);
    if (!snap) {
      // 首次收到 Marker：记录本地状态并扩散 Marker。
      this.log(`[${this.id}] 首次收到 Marker(${snapshotId}) 来自 ${from}，记录本地状态`);
      snap = this.recordLocalState(snapshotId);
      this.sendMarkers(snapshotId);
    }
    // 关闭 from -> this 信道：此前已记录的消息即为该信道在途状态。
    const ch = snap.channels.get(from);
    if (ch) ch.recording = false;
  }

  private recordLocalState(snapshotId: string): SnapshotRecord {
    const channels = new Map<string, ChannelState>();
    for (const peer of this.peers) {
      channels.set(peer, { from: peer, recording: true, messages: [] });
    }
    const snap: SnapshotRecord = {
      snapshotId,
      balance: this.balance,
      cutClock: this.clock,
      channels,
    };
    this.snapshots.set(snapshotId, snap);
    this.log(`[${this.id}] 记录本地状态: balance=${snap.balance} (clock=${this.clock})`);
    return snap;
  }

  private sendMarkers(snapshotId: string): void {
    for (const peer of this.peers) {
      this.network.send({ kind: 'marker', snapshotId, from: this.id, to: peer });
    }
  }

  getSnapshot(snapshotId: string): SnapshotRecord | undefined {
    return this.snapshots.get(snapshotId);
  }

  /** 本进程对该快照是否已完成（已记录本地状态且所有入信道均已关闭）。 */
  isSnapshotComplete(snapshotId: string): boolean {
    const snap = this.snapshots.get(snapshotId);
    if (!snap) return false;
    for (const ch of snap.channels.values()) if (ch.recording) return false;
    return true;
  }
}
