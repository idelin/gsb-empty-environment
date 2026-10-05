import type { Sim } from "./sim.ts";
import type { Link } from "./link.ts";
import type { AckPacket, DataPacket, Packet } from "./packets.ts";

export interface EndpointOptions {
  name: string;
  totalMessages: number; // 应用要发送的逻辑消息总数
  maxSendBuffer: number; // 发送缓冲上界（未确认消息数），达到后应用反压停发
  maxRecvWindow: number; // 接收乱序缓存上界，超出直接丢弃
  ackDelay: number; // 延迟 ACK 定时器（毫秒）
  retxBatch: number; // 单次 RTO 最多重传的消息数
}

interface SentRecord {
  lastSentAt: number;
  retransmits: number;
}

const MIN_RTO = 40;
const MAX_RTO = 2000;

// 端点同时扮演发送方与接收方；逻辑序号跨路径连续。
//
// 迁移与确认合并的核心规则：
//   一个从路径 p 到达的 ACK，其累计确认被钳制到 maxSeqSentOnPath[p]
//   （路径 p 上实际发送过的最大序号）。因此：
//   - 旧链路的迟到 ACK 仍可推进"已发送前缀"（ackedPrefix），释放发送缓冲；
//   - 但它无法确认只在新链路上发过的数据，更不能确认尚未发送的数据。
export class Endpoint {
  readonly name: string;
  activePath = 0;
  migrated = false;
  ackedPrefix = -1; // 所有 <= ackedPrefix 的序号均已被对端确认
  deliveredPrefix = 0; // 应用期望的下一个序号
  readonly delivered: number[] = []; // 应用交付序列（恰好一次验证的观察点）
  readonly stats = {
    sent: 0,
    retx: 0,
    rto: 0,
    fastRetx: 0,
    acks: 0,
    clampedAcks: 0, // 被钳制的 ACK 次数（含旧链路迟到 ACK 越界部分）
    lateAcksAfterMigration: 0, // 迁移后仍从旧链路到达的 ACK 数
    oldPathAckAdvanced: 0, // 旧链路迟到 ACK 实际推进了已发送前缀的次数
    dataRecv: 0,
    dupData: 0,
    recvDropped: 0,
    highWaterSend: 0, // 发送缓冲高水位（有界性证据）
    highWaterRecv: 0, // 接收缓存高水位
  };

  private readonly sim: Sim;
  private readonly rng: () => number;
  private readonly opts: EndpointOptions;
  private peer!: Endpoint;
  private outLinks = new Map<number, Link>();

  // ---- 发送方状态 ----
  private nextSeq = 0;
  private sendBuffer = new Map<number, SentRecord>(); // 仅保存未确认消息
  private sentOnPath = new Map<number, Set<number>>(); // 每路径已发序号集，随 ackedPrefix 裁剪
  private maxSeqSentOnPath = new Map<number, number>();
  private srtt = -1;
  private rto = 300;
  private rtoGen = 0;
  private lastCum = -1;
  private dupAcks = 0;

  // ---- 接收方状态 ----
  private recvBuffer = new Set<number>(); // 乱序缓存，容量 <= maxRecvWindow
  private ackPending = false;
  private ackPath = 0;
  private ackEchoSeq = -1;
  private ackEchoSentAt = 0;

  constructor(sim: Sim, rng: () => number, opts: EndpointOptions) {
    this.sim = sim;
    this.rng = rng;
    this.opts = opts;
    this.name = opts.name;
  }

  attach(pathId: number, link: Link): void {
    this.outLinks.set(pathId, link);
  }

  connect(peer: Endpoint): void {
    this.peer = peer;
  }

  // 迁移点：切换活跃路径；旧路径在排空期内仍可用于接收迟到 ACK。
  migrateTo(pathId: number): void {
    if (!this.outLinks.has(pathId)) throw new Error(`${this.name}: unknown path ${pathId}`);
    this.activePath = pathId;
    this.migrated = true;
  }

  start(): void {
    this.pumpApp();
  }

  done(): boolean {
    return this.ackedPrefix === this.opts.totalMessages - 1;
  }

  unackedCount(): number {
    return this.sendBuffer.size;
  }

  debugUnacked(): number[] {
    return [...this.sendBuffer.keys()].sort((a, b) => a - b);
  }

  onPacket(pkt: Packet): void {
    if (pkt.kind === "data") this.onData(pkt);
    else this.onAck(pkt);
  }

  // ---------------- 发送方 ----------------

  private pumpApp(): void {
    while (this.nextSeq < this.opts.totalMessages && this.sendBuffer.size < this.opts.maxSendBuffer) {
      this.sendData(this.nextSeq, false);
      this.nextSeq++;
    }
  }

  private sendData(seq: number, isRetx: boolean): void {
    const link = this.outLinks.get(this.activePath);
    if (!link) throw new Error(`${this.name}: no link for path ${this.activePath}`);
    let set = this.sentOnPath.get(this.activePath);
    if (!set) {
      set = new Set();
      this.sentOnPath.set(this.activePath, set);
    }
    set.add(seq);
    const prevMax = this.maxSeqSentOnPath.get(this.activePath) ?? -1;
    if (seq > prevMax) this.maxSeqSentOnPath.set(this.activePath, seq);
    const rec = this.sendBuffer.get(seq);
    if (rec) {
      rec.lastSentAt = this.sim.now;
      rec.retransmits++;
      this.stats.retx++;
    } else {
      this.sendBuffer.set(seq, { lastSentAt: this.sim.now, retransmits: 0 });
      this.stats.sent++;
    }
    const pkt: DataPacket = {
      kind: "data",
      pathId: this.activePath,
      seq,
      sentAt: this.sim.now,
      retransmit: isRetx,
    };
    link.transmit(pkt, this.sim, this.rng, (p) => this.peer.onPacket(p));
    this.armRto();
  }

  private onAck(pkt: AckPacket): void {
    this.stats.acks++;
    const path = pkt.pathId;
    const onOldPath = this.migrated && path !== this.activePath;
    if (onOldPath) this.stats.lateAcksAfterMigration++;

    // 确认合并 + 钳制：只能确认"该到达路径上实际发送过"的序号。
    const maxSent = this.maxSeqSentOnPath.get(path) ?? -1;
    const effCum = Math.min(pkt.cumAck, maxSent);
    if (effCum < pkt.cumAck) this.stats.clampedAcks++;

    // RTT 采样（Karn：重传过的包不采样）。
    const rec = this.sendBuffer.get(pkt.echoSeq);
    if (rec && rec.retransmits === 0 && pkt.echoSeq <= effCum) {
      const sample = this.sim.now - pkt.echoSentAt;
      this.srtt = this.srtt < 0 ? sample : 0.875 * this.srtt + 0.125 * sample;
      this.rto = Math.min(MAX_RTO, Math.max(MIN_RTO, 4 * this.srtt));
    }

    let newly = false;
    for (let s = this.ackedPrefix + 1; s <= effCum; s++) {
      if (this.sendBuffer.delete(s)) newly = true;
    }
    for (const s of pkt.sack) {
      if (s <= maxSent && this.sendBuffer.delete(s)) newly = true;
    }

    // 推进已发送前缀并释放内存。
    while (this.ackedPrefix + 1 < this.nextSeq && !this.sendBuffer.has(this.ackedPrefix + 1)) {
      this.ackedPrefix++;
    }
    // 裁剪每路径已发序号集，保持元数据有界。
    for (const set of this.sentOnPath.values()) {
      for (const s of set) if (s <= this.ackedPrefix) set.delete(s);
    }

    // 关键不变量：绝不能确认尚未发送的数据。
    if (this.ackedPrefix >= this.nextSeq) {
      throw new Error(`${this.name}: acked unsent data (prefix=${this.ackedPrefix}, nextSeq=${this.nextSeq})`);
    }

    if (onOldPath && newly) this.stats.oldPathAckAdvanced++;

    // 快重传：3 个重复 ACK 立即重传最老的未确认消息。
    if (effCum <= this.lastCum && !newly) {
      this.dupAcks++;
      if (this.dupAcks >= 3 && this.sendBuffer.size > 0) {
        this.dupAcks = 0;
        this.stats.fastRetx++;
        const oldest = Math.min(...this.sendBuffer.keys());
        this.sendData(oldest, true);
      }
    } else {
      this.dupAcks = 0;
    }
    this.lastCum = Math.max(this.lastCum, effCum);

    if (newly) {
      this.pumpApp();
      if (this.sendBuffer.size === 0) this.rtoGen++;
      else this.armRto();
    }
    this.checkMemory();
  }

  private armRto(): void {
    const g = ++this.rtoGen;
    const wait = this.rto;
    this.sim.schedule(wait, () => {
      if (g === this.rtoGen) this.onRto();
    });
  }

  private onRto(): void {
    if (this.sendBuffer.size === 0) return;
    this.stats.rto++;
    this.rto = Math.min(MAX_RTO, this.rto * 2);
    let n = 0;
    for (const seq of [...this.sendBuffer.keys()].sort((a, b) => a - b)) {
      if (n++ >= this.opts.retxBatch) break;
      this.sendData(seq, true); // 重传总是走当前活跃路径（迁移后在新链路上补发）
    }
  }

  // ---------------- 接收方 ----------------

  private onData(pkt: DataPacket): void {
    this.stats.dataRecv++;
    if (pkt.seq < this.deliveredPrefix || this.recvBuffer.has(pkt.seq)) {
      this.stats.dupData++; // 重复包：不再次交付，但仍立即 ACK
      this.scheduleAck(pkt, true);
      return;
    }
    if (pkt.seq >= this.deliveredPrefix + this.opts.maxRecvWindow) {
      this.stats.recvDropped++; // 超出接收窗口：丢弃，保持内存有界
      this.scheduleAck(pkt, true);
      return;
    }
    if (pkt.seq === this.deliveredPrefix) {
      this.delivered.push(pkt.seq); // 恰好一次：只有连续前缀才交付应用
      this.deliveredPrefix = pkt.seq + 1;
      while (this.recvBuffer.has(this.deliveredPrefix)) {
        this.recvBuffer.delete(this.deliveredPrefix);
        this.delivered.push(this.deliveredPrefix);
        this.deliveredPrefix++;
      }
      this.scheduleAck(pkt, false); // 按序到达：延迟 ACK
    } else {
      this.recvBuffer.add(pkt.seq); // 乱序：缓存并立即 ACK（带 SACK）
      this.scheduleAck(pkt, true);
    }
    this.checkMemory();
  }

  private scheduleAck(pkt: DataPacket, immediate: boolean): void {
    if (immediate) {
      this.sendAck(pkt.pathId, pkt.seq, pkt.sentAt);
      return;
    }
    if (this.ackPending) return; // 定时器已在走，触发时会带上最新 cumAck
    this.ackPending = true;
    this.ackPath = pkt.pathId; // 回复路径 = 触发包到达的路径（旧链路的延迟 ACK 由此产生）
    this.ackEchoSeq = pkt.seq;
    this.ackEchoSentAt = pkt.sentAt;
    this.sim.schedule(this.opts.ackDelay, () => {
      if (!this.ackPending) return;
      this.ackPending = false;
      this.sendAck(this.ackPath, this.ackEchoSeq, this.ackEchoSentAt);
    });
  }

  private sendAck(pathId: number, echoSeq: number, echoSentAt: number): void {
    const link = this.outLinks.get(pathId);
    if (!link) return;
    const sack = [...this.recvBuffer].sort((a, b) => a - b).slice(0, 16);
    const ack: AckPacket = {
      kind: "ack",
      pathId,
      cumAck: this.deliveredPrefix - 1,
      sack,
      echoSeq,
      echoSentAt,
    };
    link.transmit(ack, this.sim, this.rng, (p) => this.peer.onPacket(p));
  }

  // ---------------- 有界内存自检 ----------------

  private checkMemory(): void {
    if (this.sendBuffer.size > this.stats.highWaterSend) this.stats.highWaterSend = this.sendBuffer.size;
    if (this.recvBuffer.size > this.stats.highWaterRecv) this.stats.highWaterRecv = this.recvBuffer.size;
    if (this.sendBuffer.size > this.opts.maxSendBuffer) {
      throw new Error(`${this.name}: send buffer overflow (${this.sendBuffer.size})`);
    }
    if (this.recvBuffer.size > this.opts.maxRecvWindow) {
      throw new Error(`${this.name}: recv window overflow (${this.recvBuffer.size})`);
    }
    let meta = 0;
    for (const s of this.sentOnPath.values()) meta += s.size;
    if (meta > this.opts.maxSendBuffer + this.opts.maxRecvWindow) {
      throw new Error(`${this.name}: path-sent metadata unbounded (${meta})`);
    }
  }
}
