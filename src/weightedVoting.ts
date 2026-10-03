/**
 * 带权重投票的协同决策无冲突选择器（CRDT）。
 *
 * 模型：
 * - 每个节点持有 投票表(voter -> 选票) 与 权重表(voter -> 权重) 两张
 *   Last-Writer-Wins 注册表集合，时间戳为 Lamport 时钟，冲突时以
 *   (clock, nodeId) 字典序决胜，保证合并满足交换律/结合律/幂等律。
 * - 权重配置随时间因果动态调整：每次权重变更携带因果时间戳，
 *   网络愈合合并后自动收敛到因果上最新的权重配置。
 * - 计票时使用合并收敛后的最新权重：某投票人的票按其在权重表中的
 *   当前有效权重计入 赞成/反对/弃权 桶；未投票者的权重计入 pending。
 * - 决策规则（确定性）：
 *     yes > total/2            => APPROVED（赞成权重超过全网有效总权重一半）
 *     yes + pending <= total/2 => REJECTED（剩余票全投赞成也无法过半）
 *     其它                     => PENDING
 */

export type VoteChoice = 'YES' | 'NO' | 'ABSTAIN';
export type Decision = 'APPROVED' | 'REJECTED' | 'PENDING';

interface LWWEntry<T> {
  value: T;
  clock: number;
  nodeId: string;
}

export interface Tally {
  totalWeight: number;
  yesWeight: number;
  noWeight: number;
  abstainWeight: number;
  pendingWeight: number;
  abstainCount: number;
  votedCount: number;
  decision: Decision;
  weights: Record<string, number>;
  votes: Record<string, VoteChoice>;
}

interface Snapshot {
  nodeId: string;
  lamport: number;
  votes: Record<string, LWWEntry<VoteChoice>>;
  weights: Record<string, LWWEntry<number>>;
}

function compareEntries(a: LWWEntry<unknown>, b: LWWEntry<unknown>): number {
  if (a.clock !== b.clock) return a.clock - b.clock;
  if (a.nodeId === b.nodeId) return 0;
  return a.nodeId < b.nodeId ? -1 : 1;
}

function mergeInto<T>(
  target: Map<string, LWWEntry<T>>,
  source: Map<string, LWWEntry<T>>,
): void {
  for (const [key, entry] of source) {
    const existing = target.get(key);
    if (!existing || compareEntries(entry, existing) > 0) {
      target.set(key, { ...entry });
    }
  }
}

export class WeightedVotingCRDT {
  readonly nodeId: string;
  private lamport = 0;
  private votes = new Map<string, LWWEntry<VoteChoice>>();
  private weights = new Map<string, LWWEntry<number>>();

  constructor(nodeId: string) {
    if (!nodeId) throw new Error('nodeId must be non-empty');
    this.nodeId = nodeId;
  }

  private nextClock(): number {
    return ++this.lamport;
  }

  /** 本节点为自己投票（赞成/反对/弃权），重复投票以因果上最新者为准。 */
  vote(choice: VoteChoice): void {
    this.votes.set(this.nodeId, {
      value: choice,
      clock: this.nextClock(),
      nodeId: this.nodeId,
    });
  }

  /** 动态调整某投票人的权重（因果时间戳保证愈合后收敛）。 */
  setWeight(voterId: string, weight: number): void {
    if (!Number.isFinite(weight) || weight < 0) {
      throw new Error(`invalid weight for ${voterId}: ${weight}`);
    }
    this.weights.set(voterId, {
      value: weight,
      clock: this.nextClock(),
      nodeId: this.nodeId,
    });
  }

  /** 合并另一节点状态（就地合并，满足交换/结合/幂等）。 */
  merge(other: WeightedVotingCRDT): this {
    this.lamport = Math.max(this.lamport, other.lamport);
    mergeInto(this.votes, other.votes);
    mergeInto(this.weights, other.weights);
    return this;
  }

  clone(): WeightedVotingCRDT {
    return WeightedVotingCRDT.fromSnapshot(this.snapshot());
  }

  /** 当前全网计票结果与确定性决策。 */
  tally(): Tally {
    const weights: Record<string, number> = {};
    const votes: Record<string, VoteChoice> = {};
    let totalWeight = 0;
    let yesWeight = 0;
    let noWeight = 0;
    let abstainWeight = 0;
    let pendingWeight = 0;
    let abstainCount = 0;
    let votedCount = 0;

    const voterIds = [...this.weights.keys()].sort();
    for (const voterId of voterIds) {
      const weight = this.weights.get(voterId)!.value;
      if (weight <= 0) continue;
      weights[voterId] = weight;
      totalWeight += weight;
      const vote = this.votes.get(voterId);
      if (!vote) {
        pendingWeight += weight;
        continue;
      }
      votes[voterId] = vote.value;
      votedCount += 1;
      if (vote.value === 'YES') yesWeight += weight;
      else if (vote.value === 'NO') noWeight += weight;
      else {
        abstainWeight += weight;
        abstainCount += 1;
      }
    }

    let decision: Decision;
    if (totalWeight > 0 && yesWeight > totalWeight / 2) {
      decision = 'APPROVED';
    } else if (totalWeight > 0 && yesWeight + pendingWeight <= totalWeight / 2) {
      decision = 'REJECTED';
    } else {
      decision = 'PENDING';
    }

    return {
      totalWeight,
      yesWeight,
      noWeight,
      abstainWeight,
      pendingWeight,
      abstainCount,
      votedCount,
      decision,
      weights,
      votes,
    };
  }

  /** 序列化为可 JSON 传输的快照（模拟网络 gossip 消息）。 */
  snapshot(): Snapshot {
    const votes: Snapshot['votes'] = {};
    const weights: Snapshot['weights'] = {};
    for (const [k, v] of this.votes) votes[k] = { ...v };
    for (const [k, v] of this.weights) weights[k] = { ...v };
    return { nodeId: this.nodeId, lamport: this.lamport, votes, weights };
  }

  static fromSnapshot(snapshot: Snapshot): WeightedVotingCRDT {
    const replica = new WeightedVotingCRDT(snapshot.nodeId);
    replica.lamport = snapshot.lamport;
    for (const [k, v] of Object.entries(snapshot.votes)) {
      replica.votes.set(k, { ...v });
    }
    for (const [k, v] of Object.entries(snapshot.weights)) {
      replica.weights.set(k, { ...v });
    }
    return replica;
  }

  /** 通过 JSON 传输合并（等价于跨网络收到对端状态）。 */
  mergeSnapshot(snapshot: Snapshot): this {
    return this.merge(WeightedVotingCRDT.fromSnapshot(snapshot));
  }
}
