// 端点：实现“双方确认的新代次握手”。
//
// 持久状态（模拟磁盘，重启后保留）：
//   gen             本地代次号，单调递增，每次重启 +1 并立即落盘
//   maxPeerGenSeen  已知对端最大代次号，用于识别迟到的旧 RESET
//
// 关键不变式：任何来自对端旧 incarnation 的报文，其 gen 必然小于对端当前
// 代次；因此 ESTABLISHED 后只需比对 DATA.gen === 已协商的 peerGen，即可
// 把旧连接残包（序号已归零、看似合法）全部拒之门外。

import type { ConfirmMsg, DataMsg, Message, ResetAckMsg, ResetMsg } from './messages.ts';
import type { Network } from './network.ts';

export type State = 'IDLE' | 'RESET_SENT' | 'ACK_SENT' | 'ESTABLISHED';

export interface EndpointOptions {
  recvBufferCap?: number;   // 握手期间/乱序数据的接收缓冲上限（包数）
  sendQueueCap?: number;    // 握手期间应用数据的发送排队上限（包数）
  retransmitMs?: number;    // 握手报文重传间隔
  maxRetries?: number;      // 握手报文最大重传次数
  checkGeneration?: boolean; // false = 朴素模式（不校验代次，用于演示危害）
  baseDelay?: number;       // 发送报文的网络时延
}

export interface DeliveryRecord {
  gen: number;
  seq: number;
  payload: string;
  state: State;   // 交付时刻的状态机状态（安全审计用）
  peerGen: number; // 交付时刻已协商的对端代次
}

export class Endpoint {
  readonly id: string;
  readonly net: Network;
  peer: Endpoint | null = null;

  readonly disk = { gen: 0, maxPeerGenSeen: 0 };

  state: State = 'IDLE';
  private localNonce = '';
  private peerGen = 0;
  private peerNonce = '';
  private attempt = 0;
  private txSeq = 0;
  private expectedSeq = 0;
  private sendQueue: string[] = [];
  private recvBuffer: DataMsg[] = [];
  private timerId: number | null = null;
  private retries = 0;

  readonly delivered: DeliveryRecord[] = [];
  readonly stats = {
    staleReset: 0,       // 迟到的旧代次 RESET
    staleHandshake: 0,   // 不匹配的 RESET_ACK / CONFIRM
    staleData: 0,        // 旧代次数据
    dupData: 0,
    bufferDrops: 0,      // 接收缓冲溢出丢弃
    queueDrops: 0,       // 发送队列溢出丢弃
    retransmits: 0,
    implicitConfirms: 0, // 由首个数据包完成的隐式确认
  };
  readonly opts: Required<EndpointOptions>;

  constructor(id: string, net: Network, opts: EndpointOptions = {}) {
    this.id = id;
    this.net = net;
    this.opts = {
      recvBufferCap: opts.recvBufferCap ?? 4,
      sendQueueCap: opts.sendQueueCap ?? 4,
      retransmitMs: opts.retransmitMs ?? 10,
      maxRetries: opts.maxRetries ?? 5,
      checkGeneration: opts.checkGeneration ?? true,
      baseDelay: opts.baseDelay ?? 1,
    };
  }

  get negotiatedPeerGen(): number {
    return this.peerGen;
  }

  connect(peer: Endpoint): void {
    this.peer = peer;
    if (!peer.peer) peer.peer = this;
    if (this.disk.gen === 0) this.disk.gen = 1;
    this.beginHandshake();
  }

  restart(): void {
    // 崩溃重启：代次 +1 落盘，易失状态全部丢失，随后发起新代次握手
    this.disk.gen += 1;
    const savedPeer = this.peer;
    this.clearVolatile();
    this.peer = savedPeer;
    this.beginHandshake();
  }

  sendData(payload: string): void {
    if (this.state === 'ESTABLISHED') {
      this.send({ kind: 'DATA', gen: this.disk.gen, seq: this.txSeq++, payload });
    } else if (this.sendQueue.length < this.opts.sendQueueCap) {
      this.sendQueue.push(payload); // 握手期间排队，有上限
    } else {
      this.stats.queueDrops += 1;
    }
  }

  receive(msg: Message): void {
    switch (msg.kind) {
      case 'RESET':
        this.onReset(msg);
        break;
      case 'RESET_ACK':
        this.onResetAck(msg);
        break;
      case 'CONFIRM':
        this.onConfirm(msg);
        break;
      case 'DATA':
        this.onData(msg);
        break;
    }
  }

  private clearVolatile(): void {
    this.state = 'IDLE';
    this.localNonce = '';
    this.peerGen = 0;
    this.peerNonce = '';
    this.txSeq = 0;
    this.expectedSeq = 0;
    this.sendQueue = [];
    this.recvBuffer = [];
    this.clearTimer();
    this.retries = 0;
  }

  private beginHandshake(): void {
    if (!this.peer) throw new Error(`${this.id}: no peer`);
    this.newNonce();
    this.state = 'RESET_SENT';
    this.retries = 0;
    this.send({ kind: 'RESET', gen: this.disk.gen, nonce: this.localNonce });
    this.armTimer();
  }

  private newNonce(): void {
    this.attempt += 1;
    this.localNonce = `${this.id}-g${this.disk.gen}-a${this.attempt}`;
  }

  private send(msg: Message): void {
    this.net.transmit(this.id, this.peer!, msg, this.opts.baseDelay);
  }

  private sendResetAck(): void {
    this.send({
      kind: 'RESET_ACK',
      gen: this.disk.gen,
      nonce: this.localNonce,
      ackGen: this.peerGen,
      ackNonce: this.peerNonce,
    });
  }

  private armTimer(): void {
    this.clearTimer();
    this.timerId = this.net.setTimer(this.opts.retransmitMs, () => this.onTimeout());
  }

  private clearTimer(): void {
    if (this.timerId !== null) {
      this.net.clearTimer(this.timerId);
      this.timerId = null;
    }
  }

  private onTimeout(): void {
    this.timerId = null;
    if (this.state !== 'RESET_SENT' && this.state !== 'ACK_SENT') return;
    if (this.retries >= this.opts.maxRetries) {
      this.state = 'IDLE';
      return;
    }
    this.retries += 1;
    this.stats.retransmits += 1;
    if (this.state === 'RESET_SENT') {
      this.send({ kind: 'RESET', gen: this.disk.gen, nonce: this.localNonce });
    } else {
      this.sendResetAck();
    }
    this.armTimer();
  }

  private onReset(m: ResetMsg): void {
    if (m.gen < this.disk.maxPeerGenSeen) {
      this.stats.staleReset += 1; // 旧代次的迟到 RESET，直接忽略
      return;
    }
    if (
      m.gen === this.peerGen &&
      m.nonce === this.peerNonce &&
      (this.state === 'ACK_SENT' || this.state === 'ESTABLISHED')
    ) {
      this.sendResetAck(); // 当前代次的重复 RESET：幂等重答，不动状态
      return;
    }
    if (m.gen <= this.disk.maxPeerGenSeen) {
      this.stats.staleReset += 1; // 同代次但 nonce 不符：异常报文，忽略
      return;
    }
    // 对端进入新代次（对端重启或同时重启）
    this.disk.maxPeerGenSeen = m.gen;
    this.peerGen = m.gen;
    this.peerNonce = m.nonce;
    if (this.state === 'ESTABLISHED') {
      // 拆除旧连接：旧代次缓冲作废，序号空间归零
      this.recvBuffer = [];
      this.expectedSeq = 0;
      this.txSeq = 0;
    }
    if (this.disk.gen === 0) this.disk.gen = 1;
    if (this.localNonce === '') this.newNonce();
    this.state = 'ACK_SENT';
    this.retries = 0;
    this.sendResetAck();
    this.armTimer();
  }

  private onResetAck(m: ResetAckMsg): void {
    const match =
      this.localNonce !== '' && m.ackGen === this.disk.gen && m.ackNonce === this.localNonce;
    if (!match) {
      this.stats.staleHandshake += 1;
      return;
    }
    if (this.state === 'ESTABLISHED') {
      // 对端重传的 RESET_ACK：重发 CONFIRM 帮助其收敛
      this.send({
        kind: 'CONFIRM',
        gen: this.disk.gen,
        nonce: this.localNonce,
        ackGen: this.peerGen,
        ackNonce: this.peerNonce,
      });
      return;
    }
    if (this.state !== 'RESET_SENT' && this.state !== 'ACK_SENT') {
      this.stats.staleHandshake += 1;
      return;
    }
    // 对端已确认我方代次：记录对端代次并回 CONFIRM 完成相互确认
    this.peerGen = m.gen;
    this.peerNonce = m.nonce;
    if (m.gen > this.disk.maxPeerGenSeen) this.disk.maxPeerGenSeen = m.gen;
    this.send({
      kind: 'CONFIRM',
      gen: this.disk.gen,
      nonce: this.localNonce,
      ackGen: m.gen,
      ackNonce: m.nonce,
    });
    this.establish();
  }

  private onConfirm(m: ConfirmMsg): void {
    const match =
      this.localNonce !== '' && m.ackGen === this.disk.gen && m.ackNonce === this.localNonce;
    if (!match) {
      this.stats.staleHandshake += 1;
      return;
    }
    if (this.state === 'ACK_SENT') this.establish();
    // ESTABLISHED 下的重复 CONFIRM 直接忽略
  }

  private establish(): void {
    this.state = 'ESTABLISHED';
    this.clearTimer();
    this.retries = 0;
    this.expectedSeq = 0;
    this.txSeq = 0;
    // 握手期间缓冲的数据按新代次过滤，旧代次一律丢弃
    if (this.opts.checkGeneration) {
      const keep: DataMsg[] = [];
      for (const d of this.recvBuffer) {
        if (d.gen === this.peerGen) keep.push(d);
        else this.stats.staleData += 1;
      }
      this.recvBuffer = keep;
    }
    // 发送侧排队数据盖上新代次与从 0 开始的序号发出
    const queued = this.sendQueue;
    this.sendQueue = [];
    for (const payload of queued) {
      this.send({ kind: 'DATA', gen: this.disk.gen, seq: this.txSeq++, payload });
    }
    this.drainBuffer();
  }

  private onData(m: DataMsg): void {
    // 隐式确认：CONFIRM 丢失时，首个新代次数据（seq=0）证明对端已收到
    // 我们的 RESET_ACK，可据此完成握手
    if (this.state === 'ACK_SENT' && m.gen === this.peerGen && m.seq === 0) {
      this.stats.implicitConfirms += 1;
      this.establish();
    }
    if (this.state === 'IDLE') {
      this.stats.staleData += 1;
      return;
    }
    if (this.state !== 'ESTABLISHED') {
      this.bufferData(m); // 握手期间只能进入有界缓冲，绝不交付
      return;
    }
    if (this.opts.checkGeneration && m.gen !== this.peerGen) {
      this.stats.staleData += 1; // 旧连接残包：代次不符，拒绝
      return;
    }
    if (m.seq < this.expectedSeq) {
      this.stats.dupData += 1;
      return;
    }
    if (m.seq > this.expectedSeq) {
      this.bufferData(m); // 乱序：进入同一个有界缓冲
      return;
    }
    this.deliver(m);
    this.expectedSeq += 1;
    this.drainBuffer();
  }

  private bufferData(m: DataMsg): void {
    if (this.recvBuffer.some((d) => d.seq === m.seq && d.gen === m.gen)) {
      this.stats.dupData += 1;
      return;
    }
    if (this.recvBuffer.length >= this.opts.recvBufferCap) {
      this.stats.bufferDrops += 1; // 缓冲有上限：溢出即丢弃，绝不无界堆积
      return;
    }
    this.recvBuffer.push(m);
  }

  private drainBuffer(): void {
    for (;;) {
      const idx = this.recvBuffer.findIndex(
        (d) => d.seq === this.expectedSeq && (!this.opts.checkGeneration || d.gen === this.peerGen),
      );
      if (idx < 0) return;
      const [d] = this.recvBuffer.splice(idx, 1);
      this.deliver(d);
      this.expectedSeq += 1;
    }
  }

  private deliver(m: DataMsg): void {
    this.delivered.push({
      gen: m.gen,
      seq: m.seq,
      payload: m.payload,
      state: this.state,
      peerGen: this.peerGen,
    });
  }
}
