import type { Message, MessageKind, Role } from './types.ts';
import type { Simulation } from './simulation.ts';

/**
 * 租约节点状态机：follower -> candidate -> primary。
 * - 租约获取与连任都需要多数派确认（GrantLease / HeartbeatAck）。
 * - 租约到期且未获多数派续期时，主节点主动降级为候选者。
 * - 写操作受漂移栅栏保护：本地时间 >= leaseExpiry - maxClockDrift 即拒绝写入。
 */
export class LeaseNode {
  role: Role = 'follower';
  epoch = 0;
  /** primary：自身租约到期 tick（排他上界） */
  leaseExpiry = 0;
  /** follower：承诺不投票给他人的截止 tick */
  promisedUntil = 0;
  votedFor: string | null = null;
  votedEpoch = 0;
  /** 最后一次收到有效主/候选消息时的本地 tick */
  lastContact = 0;

  private grants = new Set<string>();
  private pendingLeaseEnd = 0;
  private nextHeartbeatTick = 0;
  private nextCampaignTick = 0;
  private roundAcks = new Map<number, Set<string>>();
  private renewedRounds = new Set<number>();

  readonly id: string;
  private sim: Simulation;

  constructor(id: string, sim: Simulation) {
    this.id = id;
    this.sim = sim;
  }

  private get config() {
    return this.sim.config;
  }

  /** 本地时钟 = 全局 tick + 有界偏移 */
  get localNow(): number {
    return this.sim.tick + this.config.clockOffsets[this.id];
  }

  /** 是否具备写能力（漂移栅栏内且持有租约） */
  isWriteCapable(): boolean {
    return (
      this.role === 'primary' &&
      this.localNow < this.leaseExpiry - this.config.maxClockDrift
    );
  }

  /** 发起竞选/租约申请；retry 时不增加 epoch（重发同一轮请求） */
  campaign(retry = false): void {
    if (!retry) this.epoch += 1;
    this.role = 'candidate';
    this.votedFor = this.id;
    this.votedEpoch = this.epoch;
    this.grants = new Set([this.id]);
    const leaseStart =
      this.sim.tick + this.config.forwardLatency + this.config.ackLatency;
    this.pendingLeaseEnd = leaseStart + this.config.leaseDuration;
    this.nextCampaignTick = this.sim.tick + this.config.campaignRetryInterval;
    this.sim.log(
      this.id,
      'campaign',
      `epoch ${this.epoch}, requesting lease [${leaseStart}, ${this.pendingLeaseEnd})`,
    );
    this.broadcast('RequestLease', {
      epoch: this.epoch,
      leaseStart,
      leaseEnd: this.pendingLeaseEnd,
    });
  }

  onMessage(msg: Message): void {
    switch (msg.kind) {
      case 'RequestLease':
        this.onRequestLease(msg);
        break;
      case 'GrantLease':
        this.onGrantLease(msg);
        break;
      case 'RejectLease':
        this.onRejectLease(msg);
        break;
      case 'Heartbeat':
        this.onHeartbeat(msg);
        break;
      case 'HeartbeatAck':
        this.onHeartbeatAck(msg);
        break;
    }
  }

  onTick(): void {
    if (this.role === 'primary') {
      if (this.localNow >= this.leaseExpiry) {
        this.sim.log(
          this.id,
          'demoted',
          `lease expired at ${this.leaseExpiry} without majority renewal, demoting to candidate`,
        );
        this.campaign();
      } else if (this.sim.tick >= this.nextHeartbeatTick) {
        this.sendHeartbeat();
      }
    } else if (this.role === 'candidate') {
      if (this.sim.tick >= this.nextCampaignTick) this.campaign(true);
    } else {
      const timeout = this.config.electionTimeouts[this.id];
      if (
        this.lastContact > 0 &&
        this.localNow >= this.promisedUntil &&
        this.localNow - this.lastContact >= timeout
      ) {
        this.campaign();
      }
    }
  }

  private onRequestLease(msg: Message): void {
    if (msg.epoch < this.epoch) {
      this.reject(msg, `stale epoch ${msg.epoch} < ${this.epoch}`);
      return;
    }
    // 持有未过期租约的主节点绝不投票：防止旧租约未死、新租约并立
    if (this.role === 'primary' && this.localNow < this.leaseExpiry) {
      this.reject(msg, `holds lease until ${this.leaseExpiry}`);
      return;
    }
    const freshEpoch = msg.epoch > this.votedEpoch;
    const reusable =
      msg.epoch === this.votedEpoch &&
      (this.votedFor === null || this.votedFor === msg.from);
    if ((!freshEpoch && !reusable) || this.localNow < this.promisedUntil) {
      const why =
        this.localNow < this.promisedUntil
          ? `lease promised until ${this.promisedUntil}`
          : `vote already cast for ${this.votedFor} in epoch ${this.votedEpoch}`;
      this.reject(msg, why);
      return;
    }
    this.epoch = msg.epoch;
    this.votedEpoch = msg.epoch;
    this.votedFor = msg.from;
    this.promisedUntil = msg.leaseEnd!;
    this.lastContact = this.localNow;
    this.role = 'follower';
    this.sim.log(
      this.id,
      'vote-granted',
      `granted vote to ${msg.from} for epoch ${msg.epoch}, promised until ${msg.leaseEnd}`,
    );
    this.sim.send(this.id, msg.from, 'GrantLease', { epoch: msg.epoch });
  }

  private onGrantLease(msg: Message): void {
    if (this.role !== 'candidate' || msg.epoch !== this.epoch) return;
    this.grants.add(msg.from);
    if (this.grants.size >= this.sim.majority) {
      this.role = 'primary';
      this.leaseExpiry = this.pendingLeaseEnd;
      this.sim.log(
        this.id,
        'lease-acquired',
        `epoch ${this.epoch}, majority {${[...this.grants].sort().join(',')}}, lease [${this.sim.tick}, ${this.leaseExpiry})`,
      );
      this.sendHeartbeat();
    }
  }

  private onRejectLease(msg: Message): void {
    if (msg.epoch >= this.epoch && this.role !== 'follower') {
      this.sim.log(
        this.id,
        'step-down',
        `rejected by ${msg.from} (epoch ${msg.epoch}), becoming follower`,
      );
      this.role = 'follower';
      this.epoch = msg.epoch;
    }
  }

  private onHeartbeat(msg: Message): void {
    if (msg.epoch < this.epoch) {
      this.sim.log(
        this.id,
        'request-rejected',
        `stale Heartbeat from ${msg.from} (epoch ${msg.epoch} < ${this.epoch})`,
      );
      return;
    }
    if (this.role !== 'follower') {
      this.sim.log(
        this.id,
        'step-down',
        `recognized primary ${msg.from} for epoch ${msg.epoch}, lease until ${msg.leaseEnd}`,
      );
    }
    this.epoch = msg.epoch;
    this.role = 'follower';
    this.promisedUntil = msg.leaseEnd!;
    this.lastContact = this.localNow;
    this.sim.send(this.id, msg.from, 'HeartbeatAck', {
      epoch: msg.epoch,
      round: msg.round,
    });
  }

  private onHeartbeatAck(msg: Message): void {
    if (this.role !== 'primary' || msg.epoch !== this.epoch) return;
    if (msg.round === undefined) return;
    const acks = this.roundAcks.get(msg.round);
    if (!acks) return;
    acks.add(msg.from);
    // 续期条件：本轮心跳收到多数派应答（含自身）
    if (
      acks.size + 1 >= this.sim.majority &&
      !this.renewedRounds.has(msg.round)
    ) {
      this.renewedRounds.add(msg.round);
      this.leaseExpiry = Math.max(
        this.leaseExpiry,
        this.sim.tick + this.config.leaseDuration,
      );
      this.sim.log(
        this.id,
        'lease-renewed',
        `majority heartbeat acks (round ${msg.round}), lease extended to ${this.leaseExpiry}`,
      );
    }
  }

  private reject(msg: Message, why: string): void {
    this.sim.log(
      this.id,
      'request-rejected',
      `stale ${msg.kind} from ${msg.from} (epoch ${msg.epoch}): ${why}`,
    );
    this.sim.send(this.id, msg.from, 'RejectLease', {
      epoch: Math.max(this.epoch, msg.epoch),
    });
  }

  private sendHeartbeat(): void {
    this.nextHeartbeatTick = this.sim.tick + this.config.heartbeatInterval;
    this.roundAcks.set(this.sim.tick, new Set());
    this.broadcast('Heartbeat', {
      epoch: this.epoch,
      leaseEnd: this.leaseExpiry,
      round: this.sim.tick,
    });
  }

  private broadcast(kind: MessageKind, fields: Partial<Message>): void {
    for (const id of this.config.nodeIds) {
      if (id !== this.id) this.sim.send(this.id, id, kind, fields);
    }
  }
}
