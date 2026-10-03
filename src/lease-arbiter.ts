/**
 * 多数派租约仲裁协议（Quorum Lease Arbitration）
 *
 * 核心不变式：
 * 1. 一个节点要成为 Leader，必须获得集群成员中超过半数（Quorum）节点的租约授权。
 * 2. 每个仲裁节点在同一任期内、且已授权租约未过期前，不会把租约授权给其他候选者。
 * 3. Leader 只有在持有未过期租约时才接受写请求；租约过期后自动剥离主权限。
 * 4. 由于任意两个 Quorum 必有交集，且交集中的节点在租约过期前不会重复授权，
 *    因此在任意时刻不会存在两个同时持有有效租约的 Leader —— 杜绝脑裂。
 */

export type NodeId = string;

/** 一份租约授权：某仲裁节点授予 holder 在 expiresAt 之前、任期 term 内的领导权 */
export interface LeaseGrant {
  term: number;
  holder: NodeId;
  expiresAt: number;
}

export interface RequestLeaseMsg {
  type: "request-lease";
  term: number;
  candidateId: NodeId;
  leaseDurationMs: number;
}

export interface RequestLeaseReply {
  granted: boolean;
  term: number;
  /** 授权到期时间（拒绝时为 0） */
  expiresAt: number;
  from: NodeId;
  reason?: string;
}

export class NotLeaderError extends Error {
  constructor(id: NodeId, reason = "not leader") {
    super(`node ${id} rejected write: ${reason}`);
    this.name = "NotLeaderError";
  }
}

/** 消息投递通道抽象，由 Cluster 实现（负责模拟网络分区） */
export interface Transport {
  send(from: NodeId, to: NodeId, msg: RequestLeaseMsg): RequestLeaseReply | null;
  now(): number;
  quorumSize(): number;
  memberIds(): NodeId[];
}

export class ArbiterNode {
  readonly id: NodeId;
  private readonly transport: Transport;

  /** 本节点作为仲裁者已授予他人的租约 */
  private grantedLease: LeaseGrant | null = null;
  private maxTermSeen = 0;

  /** 本节点作为 Leader 的状态 */
  private leader = false;
  private leaderTerm = 0;
  private leaseExpiresAt = 0;
  private readonly committedLog: string[] = [];

  constructor(id: NodeId, transport: Transport) {
    this.id = id;
    this.transport = transport;
  }

  get isLeader(): boolean {
    return this.leader && this.transport.now() < this.leaseExpiresAt;
  }

  get term(): number {
    return this.leaderTerm;
  }

  get leaseExpiry(): number {
    return this.leaseExpiresAt;
  }

  get log(): readonly string[] {
    return this.committedLog;
  }

  /** 当前已授予他人的租约（测试/调试用） */
  get granted(): LeaseGrant | null {
    return this.grantedLease;
  }

  /**
   * 发起竞选：向所有成员请求租约授权，
   * 凑齐 Quorum 授权即成为 Leader，租约有效期取所有授权的最短到期时间。
   */
  campaign(leaseDurationMs: number): boolean {
    this.maxTermSeen += 1;
    const term = this.maxTermSeen;
    const grants = this.collectGrants(term, leaseDurationMs);
    if (grants.length < this.transport.quorumSize()) {
      return false;
    }
    this.leader = true;
    this.leaderTerm = term;
    this.leaseExpiresAt = Math.min(...grants.map((g) => g.expiresAt));
    return true;
  }

  /**
   * 续订租约：以当前任期重新向 Quorum 请求授权。
   * 成功则延长租约；失败不立即卸任，但租约到期后自动丧失主权限。
   */
  renewLease(leaseDurationMs: number): boolean {
    if (!this.leader) return false;
    const grants = this.collectGrants(this.leaderTerm, leaseDurationMs);
    const now = this.transport.now();
    const valid = grants.filter((g) => g.expiresAt > now);
    if (valid.length < this.transport.quorumSize()) {
      return false;
    }
    this.leaseExpiresAt = Math.min(...valid.map((g) => g.expiresAt));
    return true;
  }

  /** 写请求：只有持有有效租约的 Leader 才能受理 */
  write(value: string): void {
    if (!this.leader) throw new NotLeaderError(this.id);
    if (this.transport.now() >= this.leaseExpiresAt) {
      this.revokeLeadership();
      throw new NotLeaderError(this.id, "lease expired, leadership revoked");
    }
    this.committedLog.push(value);
  }

  /** 时钟推进回调：租约过期自动剥离主权限（无需任何网络消息） */
  onClockTick(): void {
    if (this.leader && this.transport.now() >= this.leaseExpiresAt) {
      this.revokeLeadership();
    }
  }

  private revokeLeadership(): void {
    this.leader = false;
    this.leaseExpiresAt = 0;
  }

  private collectGrants(term: number, leaseDurationMs: number): RequestLeaseReply[] {
    const msg: RequestLeaseMsg = {
      type: "request-lease",
      term,
      candidateId: this.id,
      leaseDurationMs,
    };
    const grants: RequestLeaseReply[] = [];
    for (const peerId of this.transport.memberIds()) {
      const reply = this.transport.send(this.id, peerId, msg);
      if (reply === null) continue; // 网络不可达（分区）
      if (reply.term > this.maxTermSeen) this.maxTermSeen = reply.term;
      if (reply.granted) grants.push(reply);
    }
    return grants;
  }

  /** 作为仲裁者处理租约请求（由 Cluster 在投递时调用） */
  handleRequestLease(msg: RequestLeaseMsg): RequestLeaseReply {
    const now = this.transport.now();
    if (msg.term > this.maxTermSeen) this.maxTermSeen = msg.term;

    const reject = (reason: string): RequestLeaseReply => ({
      granted: false,
      term: this.maxTermSeen,
      expiresAt: 0,
      from: this.id,
      reason,
    });

    if (msg.term < this.maxTermSeen) {
      return reject(`stale term ${msg.term} < ${this.maxTermSeen}`);
    }
    if (
      this.grantedLease !== null &&
      this.grantedLease.expiresAt > now &&
      this.grantedLease.holder !== msg.candidateId
    ) {
      // 租约未过期前，对任何候选者（含更高任期）都不重复授权 —— 脑裂防线
      return reject(`lease held by ${this.grantedLease.holder} until ${this.grantedLease.expiresAt}`);
    }

    // 授权：同一持有者可续订；已过期租约可被重新授予
    this.grantedLease = {
      term: msg.term,
      holder: msg.candidateId,
      expiresAt: now + msg.leaseDurationMs,
    };
    return {
      granted: true,
      term: msg.term,
      expiresAt: this.grantedLease.expiresAt,
      from: this.id,
    };
  }
}
