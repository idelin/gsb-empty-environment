/**
 * 带权重投票的协同决策无冲突选择器（CRDT）。
 *
 * 设计要点：
 * - 投票：每个节点对同一提案至多一条生效选票，LWW（Lamport 时钟 + 决定性次序）解决并发改票。
 * - 权重：权重变更是一条条带因果时间戳的事件（grow-only set），
 *   每张选票按其投票时刻因果可见的最新权重计权（权重随时间因果动态调整）。
 * - 合并：交换状态后取并集 / LWW，满足交换律、结合律、幂等律，
 *   网络分区愈合后所有节点收敛到同一状态，输出确定性决策。
 */

export type VoteChoice = 'yes' | 'no' | 'abstain';
export type Decision = 'approved' | 'rejected' | 'pending';

export interface VoteRecord {
  voter: string;
  choice: VoteChoice;
  clock: number;
  seq: number;
}

export interface WeightEvent {
  node: string;
  weight: number;
  clock: number;
  actor: string;
  seq: number;
}

export interface Tally {
  yesWeight: number;
  noWeight: number;
  abstainWeight: number;
  votedWeight: number;
  totalWeight: number;
  quorum: boolean;
  decision: Decision;
}

interface SerializedState {
  nodeId: string;
  clock: number;
  seq: number;
  votes: VoteRecord[];
  weightEvents: WeightEvent[];
}

const CHOICE_ORDER: Record<VoteChoice, number> = { yes: 0, no: 1, abstain: 2 };

/** 选票的决定性次序：先比 Lamport 时钟，再比序号，最后比选项（固定顺序）。 */
function compareVote(a: VoteRecord, b: VoteRecord): number {
  if (a.clock !== b.clock) return a.clock - b.clock;
  if (a.seq !== b.seq) return a.seq - b.seq;
  return CHOICE_ORDER[a.choice] - CHOICE_ORDER[b.choice];
}

/** 权重事件的决定性次序：先比时钟，再比序号，最后比发起者 id。 */
function compareWeightEvent(a: WeightEvent, b: WeightEvent): number {
  if (a.clock !== b.clock) return a.clock - b.clock;
  if (a.seq !== b.seq) return a.seq - b.seq;
  return a.actor < b.actor ? -1 : a.actor > b.actor ? 1 : 0;
}

export class WeightedVoting {
  readonly nodeId: string;
  #clock = 0;
  #seq = 0;
  #votes = new Map<string, VoteRecord>();
  #weightEvents = new Map<string, WeightEvent>();

  constructor(nodeId: string) {
    if (!nodeId) throw new Error('nodeId must be a non-empty string');
    this.nodeId = nodeId;
  }

  get clock(): number {
    return this.#clock;
  }

  #tick(): number {
    this.#clock += 1;
    this.#seq += 1;
    return this.#clock;
  }

  /** 记录一次权重变更（可针对任意节点，通常由治理节点发起）。 */
  setWeight(node: string, weight: number): WeightEvent {
    if (!Number.isFinite(weight) || weight < 0) {
      throw new Error(`invalid weight ${weight} for node ${node}`);
    }
    const clock = this.#tick();
    const event: WeightEvent = { node, weight, clock, actor: this.nodeId, seq: this.#seq };
    this.#weightEvents.set(`${event.actor}#${event.seq}`, event);
    return event;
  }

  /** 本节点投出一票；重复投票按 LWW 覆盖。 */
  vote(choice: VoteChoice): VoteRecord {
    const clock = this.#tick();
    const record: VoteRecord = { voter: this.nodeId, choice, clock, seq: this.#seq };
    const existing = this.#votes.get(this.nodeId);
    if (!existing || compareVote(record, existing) > 0) {
      this.#votes.set(this.nodeId, record);
    }
    return record;
  }

  /** 合并另一副本的状态（网络愈合时的同步入口）。幂等、可交换、可结合。 */
  merge(other: WeightedVoting): this {
    for (const [key, event] of other.#weightEvents) {
      if (!this.#weightEvents.has(key)) this.#weightEvents.set(key, event);
    }
    for (const [voter, record] of other.#votes) {
      const current = this.#votes.get(voter);
      if (!current || compareVote(record, current) > 0) {
        this.#votes.set(voter, record);
      }
    }
    this.#clock = Math.max(this.#clock, other.#clock);
    return this;
  }

  /** 某节点在指定因果时刻的有效权重（该时刻之前最近一次权重配置；无记录则为 0）。 */
  effectiveWeightAt(node: string, clock: number): number {
    let best: WeightEvent | undefined;
    for (const event of this.#weightEvents.values()) {
      if (event.node !== node || event.clock > clock) continue;
      if (!best || compareWeightEvent(event, best) > 0) best = event;
    }
    return best ? best.weight : 0;
  }

  /** 某节点当前（最新）有效权重。 */
  currentWeight(node: string): number {
    return this.effectiveWeightAt(node, Number.POSITIVE_INFINITY);
  }

  /** 全网已知节点（出现过权重配置或投过票），按字典序排列保证确定性。 */
  knownNodes(): string[] {
    const nodes = new Set<string>();
    for (const event of this.#weightEvents.values()) nodes.add(event.node);
    for (const voter of this.#votes.keys()) nodes.add(voter);
    return [...nodes].sort();
  }

  /**
   * 汇总当前状态，输出确定性决策：
   * - approved：赞成权重 > 全网有效总权重的一半；
   * - rejected：反对权重 >= 全网有效总权重的一半（平局确定性判负）；
   * - 其余为 pending（弃权计入已投票权重与法定人数，但不计入赞成/反对）。
   */
  tally(): Tally {
    let totalWeight = 0;
    for (const node of this.knownNodes()) totalWeight += this.currentWeight(node);

    let yesWeight = 0;
    let noWeight = 0;
    let abstainWeight = 0;
    const votes = [...this.#votes.values()].sort((a, b) =>
      a.voter < b.voter ? -1 : a.voter > b.voter ? 1 : 0,
    );
    for (const record of votes) {
      const weight = this.effectiveWeightAt(record.voter, record.clock);
      if (record.choice === 'yes') yesWeight += weight;
      else if (record.choice === 'no') noWeight += weight;
      else abstainWeight += weight;
    }

    const votedWeight = yesWeight + noWeight + abstainWeight;
    const quorum = totalWeight > 0 && votedWeight * 2 > totalWeight;

    let decision: Decision = 'pending';
    if (totalWeight > 0) {
      if (yesWeight * 2 > totalWeight) decision = 'approved';
      else if (noWeight * 2 >= totalWeight) decision = 'rejected';
    }

    return { yesWeight, noWeight, abstainWeight, votedWeight, totalWeight, quorum, decision };
  }

  /** 序列化为可跨网络传输的纯 JSON 状态。 */
  toJSON(): SerializedState {
    return {
      nodeId: this.nodeId,
      clock: this.#clock,
      seq: this.#seq,
      votes: [...this.#votes.values()],
      weightEvents: [...this.#weightEvents.values()],
    };
  }

  /** 从 JSON 状态恢复副本。 */
  static fromJSON(state: SerializedState): WeightedVoting {
    const replica = new WeightedVoting(state.nodeId);
    replica.#clock = state.clock;
    replica.#seq = state.seq;
    for (const record of state.votes) replica.#votes.set(record.voter, record);
    for (const event of state.weightEvents) {
      replica.#weightEvents.set(`${event.actor}#${event.seq}`, event);
    }
    return replica;
  }
}
