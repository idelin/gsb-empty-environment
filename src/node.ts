import { BoundedClock } from './clock.ts';
import type { LeaseConfig, Message, MessageType, Role } from './types.ts';

/** 节点与模拟器之间的上下文接口(避免循环依赖)。 */
export interface NodeSim {
  readonly tick: number;
  readonly config: LeaseConfig;
  readonly majority: number;
  send(type: MessageType, from: string, to: string, term: number, reason?: string): void;
  log(node: string | null, type: string, detail: string): void;
}

export interface LeaseView {
  holder: string;
  term: number;
  grantedLocal: number;
  expiryLocal: number;
}

/**
 * 主备租约状态机。
 *
 * 安全规则(时钟漂移受界 maxSkew 下):
 * - 主节点在本地时钟到达 expiry - maxSkew 时主动降级(保守停止服务),
 *   除非此前已凭多数派心跳应答完成续期;
 * - 备节点在租约视图到期后才允许授予新租约,并提前 networkDelay 发起选举,
 *   使新租约生效点不早于旧租约过期点,杜绝双主写入窗口重叠;
 * - 持有未过期租约视图的节点拒绝一切其他节点的租约请求(LEASE_HELD),
 *   任期低于本地任期的请求一律拒绝(STALE_TERM)。
 */
export class LeaseNode {
  readonly id: string;
  readonly clock: BoundedClock;
  role: Role = 'FOLLOWER';
  term = 0;
  lease: LeaseView | null = null;
  readonly writes: Array<{ tick: number; value: string }> = [];

  private ackedTerm = 0;
  private ackedFor: string | null = null;
  private readonly votes = new Set<string>();
  private readonly heartbeatAcks = new Set<string>();
  private nextHeartbeatLocal = Number.POSITIVE_INFINITY;
  private demoteAtLocal = Number.POSITIVE_INFINITY;
  private nextElectionLocal = Number.POSITIVE_INFINITY;
  private readonly sim: NodeSim;

  constructor(id: string, sim: NodeSim) {
    this.id = id;
    this.sim = sim;
    this.clock = new BoundedClock(id, sim.config.maxClockSkew);
  }

  /** 当前是否处于可安全写入的窗口(主 + 本地时钟未越过保守降级点)。 */
  get canServeWrites(): boolean {
    return this.role === 'PRIMARY' && this.lease !== null && this.localNow() < this.demoteAtLocal;
  }

  private localNow(): number {
    return this.clock.localTime(this.sim.tick);
  }

  private peers(): string[] {
    return this.sim.config.nodeIds.filter((id) => id !== this.id);
  }

  /** 在非租约持有者中的优先级排位(按 id 排序),用于选举退避。 */
  private electionRank(holder: string): number {
    const nonHolders = this.sim.config.nodeIds.filter((id) => id !== holder).sort();
    return Math.max(0, nonHolders.indexOf(this.id));
  }

  private scheduleElection(expiryLocal: number): void {
    const holder = this.lease?.holder ?? '';
    const { networkDelay, electionBackoffUnit } = this.sim.config;
    this.nextElectionLocal =
      expiryLocal - networkDelay + this.electionRank(holder) * electionBackoffUnit;
  }

  /** 发起新一轮选举(term 自增)。 */
  campaign(): void {
    this.term += 1;
    this.startCandidacy('ELECTION_START');
  }

  /** 同一 term 内重试选举(不自增 term,避免隔离节点任期膨胀干扰在任主)。 */
  private retryCampaign(): void {
    this.startCandidacy('ELECTION_RETRY');
  }

  private startCandidacy(eventType: string): void {
    const local = this.localNow();
    this.role = 'CANDIDATE';
    this.votes.clear();
    this.lease = null;
    this.demoteAtLocal = Number.POSITIVE_INFINITY;
    this.nextElectionLocal = local + this.sim.config.electionRetryInterval;
    this.sim.log(this.id, eventType, `term=${this.term} localTick=${local}`);
    for (const peer of this.peers()) {
      this.sim.send('LeaseRequest', this.id, peer, this.term);
    }
  }

  onTick(): void {
    const local = this.localNow();
    if (this.role === 'PRIMARY') {
      if (local >= this.demoteAtLocal) {
        this.sim.log(
          this.id,
          'PRIMARY_DEMOTED',
          `reason=lease_expired_without_majority_renewal term=${this.term} localTick=${local}`,
        );
        this.campaign();
        return;
      }
      if (local >= this.nextHeartbeatLocal) {
        this.nextHeartbeatLocal = local + this.sim.config.heartbeatInterval;
        this.heartbeatAcks.clear();
        this.sim.log(this.id, 'HEARTBEAT_SENT', `term=${this.term} localTick=${local}`);
        for (const peer of this.peers()) {
          this.sim.send('Heartbeat', this.id, peer, this.term);
        }
      }
      return;
    }
    if (local >= this.nextElectionLocal) {
      if (this.role === 'CANDIDATE') this.retryCampaign();
      else this.campaign();
    }
  }

  onMessage(msg: Message): void {
    const local = this.localNow();
    switch (msg.type) {
      case 'LeaseRequest':
        return this.onLeaseRequest(msg, local);
      case 'LeaseAck':
        return this.onLeaseAck(msg, local);
      case 'LeaseReject':
        return this.onLeaseReject(msg, local);
      case 'Heartbeat':
        return this.onHeartbeat(msg, local);
      case 'HeartbeatAck':
        return this.onHeartbeatAck(msg, local);
    }
  }

  private onLeaseRequest(msg: Message, local: number): void {
    const reject = (reason: string): void => {
      this.sim.send('LeaseReject', this.id, msg.from, this.term, reason);
      this.sim.log(this.id, 'LEASE_REJECT', `to=${msg.from} requestTerm=${msg.term} reason=${reason}`);
    };
    if (msg.term < this.term) {
      return reject('STALE_TERM');
    }
    if (this.lease && this.lease.holder !== msg.from && local < this.lease.expiryLocal) {
      return reject(`LEASE_HELD(holder=${this.lease.holder},expiryLocal=${this.lease.expiryLocal})`);
    }
    if (this.ackedTerm === msg.term && this.ackedFor !== null && this.ackedFor !== msg.from) {
      return reject(`ALREADY_ACKED(holder=${this.ackedFor})`);
    }
    if (msg.term > this.term) {
      this.sim.log(this.id, 'TERM_UPDATED', `${this.term}->${msg.term}`);
    }
    this.term = msg.term;
    if (this.role === 'PRIMARY') {
      this.sim.log(this.id, 'PRIMARY_DEMOTED', `reason=superseded_by_term_${msg.term}`);
    }
    this.role = 'FOLLOWER';
    this.ackedTerm = msg.term;
    this.ackedFor = msg.from;
    this.lease = {
      holder: msg.from,
      term: msg.term,
      grantedLocal: local,
      expiryLocal: local + this.sim.config.leaseTtl,
    };
    this.scheduleElection(this.lease.expiryLocal);
    this.sim.send('LeaseAck', this.id, msg.from, msg.term);
    this.sim.log(this.id, 'LEASE_ACK', `to=${msg.from} term=${msg.term} expiryLocal=${this.lease.expiryLocal}`);
  }

  private onLeaseAck(msg: Message, local: number): void {
    if (this.role !== 'CANDIDATE' || msg.term !== this.term) return;
    this.votes.add(msg.from);
    this.sim.log(
      this.id,
      'LEASE_ACK_RECEIVED',
      `from=${msg.from} term=${msg.term} votes=${this.votes.size + 1}/${this.sim.config.nodeIds.length}`,
    );
    if (this.votes.size + 1 >= this.sim.majority) {
      this.role = 'PRIMARY';
      this.lease = {
        holder: this.id,
        term: this.term,
        grantedLocal: local,
        expiryLocal: local + this.sim.config.leaseTtl,
      };
      this.demoteAtLocal = this.lease.expiryLocal - this.sim.config.maxClockSkew;
      this.nextHeartbeatLocal = local + this.sim.config.heartbeatInterval;
      this.heartbeatAcks.clear();
      const majority = [this.id, ...this.votes].sort().join(',');
      this.sim.log(
        this.id,
        'LEASE_GRANTED',
        `term=${this.term} majority=[${majority}] expiryLocal=${this.lease.expiryLocal} demoteAtLocal=${this.demoteAtLocal}`,
      );
    }
  }

  private onLeaseReject(msg: Message, local: number): void {
    const leaseValid = this.lease !== null && local < this.lease.expiryLocal;
    if (msg.term > this.term && !leaseValid) {
      this.sim.log(this.id, 'TERM_UPDATED', `${this.term}->${msg.term}`);
      this.term = msg.term;
      if (this.role === 'PRIMARY') {
        this.sim.log(this.id, 'PRIMARY_DEMOTED', 'reason=higher_term_seen');
      }
      this.role = 'FOLLOWER';
      return;
    }
    if (this.role === 'CANDIDATE' && msg.term === this.term) {
      this.sim.log(this.id, 'LEASE_REJECTED', `by=${msg.from} reason=${msg.reason ?? 'UNKNOWN'}`);
    }
  }

  private onHeartbeat(msg: Message, local: number): void {
    if (msg.term < this.term) {
      this.sim.send('LeaseReject', this.id, msg.from, this.term, 'STALE_TERM');
      this.sim.log(this.id, 'HEARTBEAT_REJECT', `to=${msg.from} heartbeatTerm=${msg.term} reason=STALE_TERM`);
      return;
    }
    if (this.role === 'PRIMARY' && msg.from !== this.id) {
      this.sim.log(this.id, 'PRIMARY_DEMOTED', `reason=peer_heartbeat_term_${msg.term}`);
    }
    if (msg.term > this.term) {
      this.sim.log(this.id, 'TERM_UPDATED', `${this.term}->${msg.term}`);
    }
    this.term = msg.term;
    if (this.role !== 'FOLLOWER') {
      this.sim.log(this.id, 'ROLE_CHANGE', `${this.role}->FOLLOWER (heartbeat from ${msg.from})`);
    }
    this.role = 'FOLLOWER';
    const grantedLocal =
      this.lease && this.lease.holder === msg.from ? this.lease.grantedLocal : local;
    this.lease = {
      holder: msg.from,
      term: msg.term,
      grantedLocal,
      expiryLocal: local + this.sim.config.leaseTtl,
    };
    this.scheduleElection(this.lease.expiryLocal);
    this.sim.send('HeartbeatAck', this.id, msg.from, msg.term);
    this.sim.log(this.id, 'HEARTBEAT_ACK', `to=${msg.from} term=${msg.term} expiryLocal=${this.lease.expiryLocal}`);
  }

  private onHeartbeatAck(msg: Message, local: number): void {
    if (this.role !== 'PRIMARY' || msg.term !== this.term || !this.lease) return;
    this.heartbeatAcks.add(msg.from);
    if (this.heartbeatAcks.size + 1 >= this.sim.majority) {
      this.lease = { ...this.lease, expiryLocal: local + this.sim.config.leaseTtl };
      this.demoteAtLocal = this.lease.expiryLocal - this.sim.config.maxClockSkew;
      this.heartbeatAcks.clear();
      this.sim.log(
        this.id,
        'LEASE_RENEWED',
        `term=${this.term} expiryLocal=${this.lease.expiryLocal} demoteAtLocal=${this.demoteAtLocal}`,
      );
    }
  }

  /** 客户端写请求:仅主节点在租约安全窗口内接受。 */
  clientWrite(value: string): boolean {
    const local = this.localNow();
    if (this.canServeWrites) {
      this.writes.push({ tick: this.sim.tick, value });
      this.sim.log(this.id, 'WRITE_COMMITTED', `value=${value} term=${this.term} localTick=${local}`);
      return true;
    }
    this.sim.log(this.id, 'WRITE_REJECTED', `value=${value} role=${this.role} localTick=${local}`);
    return false;
  }
}
