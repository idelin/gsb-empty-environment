
import { Region, sha256Hex, verifyVote } from "./region.ts";
import type {
  ArbitrationResult,
  Ballot,
  RegionConfig,
  TakeoverEvidence,
  TopologyMode,
} from "./types.ts";

export class QuorumNotReachedError extends Error {
  readonly achieved: number;
  readonly required: number;
  readonly topology: TopologyMode;

  constructor(achieved: number, required: number, topology: TopologyMode) {
    super(
      `多数派未达成：拓扑=${topology}，达成权重=${achieved}，要求权重=${required}`,
    );
    this.name = "QuorumNotReachedError";
    this.achieved = achieved;
    this.required = required;
    this.topology = topology;
  }
}

export class StaleEpochError extends Error {
  readonly presented: number;
  readonly current: number;

  constructor(presented: number, current: number) {
    super(`纪元过期：出示纪元=${presented}，当前纪元=${current}`);
    this.name = "StaleEpochError";
    this.presented = presented;
    this.current = current;
  }
}

/**
 * 分层仲裁多数派决策引擎。
 *
 * 分级规则：
 * - hierarchical（正常）：多数派门限基于全部区域权重；
 * - degraded（降级）：主中心不可达时自动切换，门限仅基于仍可达区域的权重，
 *   避免跨区网络闪断导致全局不可用；
 * - 每次拓扑切换都会推进纪元（epoch），旧纪元的决策证据一律被围栏（fencing），
 *   保证主中心恢复接管后无状态冲突。
 */
export class HierarchicalArbiter {
  private readonly regions = new Map<string, Region>();
  private readonly primaryId: string;
  private epoch = 1;
  private topology: TopologyMode = "hierarchical";
  private readonly decisionLog: ArbitrationResult[] = [];

  constructor(configs: RegionConfig[]) {
    const primaries = configs.filter((c) => c.isPrimary);
    if (primaries.length !== 1) throw new Error("必须且只能指定一个主中心");
    this.primaryId = primaries[0].id;
    for (const config of configs) {
      this.regions.set(config.id, new Region(config));
    }
  }

  get currentEpoch(): number {
    return this.epoch;
  }

  get currentTopology(): TopologyMode {
    return this.topology;
  }

  get log(): readonly ArbitrationResult[] {
    return this.decisionLog;
  }

  region(id: string): Region {
    const region = this.regions.get(id);
    if (!region) throw new Error(`未知区域：${id}`);
    return region;
  }

  /** 当前拓扑下参与仲裁的区域池 */
  private activePool(): Region[] {
    const all = [...this.regions.values()];
    return this.topology === "hierarchical" ? all : all.filter((r) => r.reachable);
  }

  /** 当前拓扑下的多数派权重门限：floor(总权重 / 2) + 1 */
  requiredWeight(): number {
    const total = this.activePool().reduce((sum, r) => sum + r.weight, 0);
    return Math.floor(total / 2) + 1;
  }

  /** 健康检查回调：更新可达性，主中心失联时自动降级 */
  setReachable(id: string, reachable: boolean): void {
    const region = this.region(id);
    const wasReachable = region.reachable;
    region.reachable = reachable;
    if (wasReachable && !reachable && region.isPrimary && this.topology === "hierarchical") {
      this.enterDegradedTopology();
    }
  }

  /** 主中心失联：降级拓扑 + 推进纪元，围栏旧纪元的一切决策 */
  private enterDegradedTopology(): void {
    this.topology = "degraded";
    this.epoch += 1;
  }

  /**
   * 主中心恢复后出示更高纪元重新接管。
   * 纪元必须严格大于当前（降级）纪元，否则拒绝，防止过期主中心脑裂。
   */
  recoverPrimary(presentedEpoch: number): TakeoverEvidence {
    const primary = this.region(this.primaryId);
    if (presentedEpoch <= this.epoch) {
      throw new StaleEpochError(presentedEpoch, this.epoch);
    }
    primary.reachable = true;
    this.epoch = presentedEpoch;
    this.topology = "hierarchical";
    const payload = `takeover:${this.primaryId}:${presentedEpoch}`;
    const vote = primary.castVote({
      epoch: presentedEpoch,
      decision: payload,
      topology: "hierarchical",
      voter: primary.id,
    });
    return {
      regionId: primary.id,
      presentedEpoch,
      signature: vote!.signature,
      publicKey: primary.publicKey,
    };
  }

  /**
   * 发起一次仲裁。
   * 每个区域先在本地达成多数派门限才投出加权票；
   * 权重合计达到当前拓扑门限则形成决议，结果携带全部签名证据。
   */
  arbitrate(decision: string): ArbitrationResult {
    const votes = [];
    for (const region of this.activePool()) {
      const ballot: Ballot = {
        epoch: this.epoch,
        decision,
        topology: this.topology,
        voter: region.id,
      };
      const vote = region.castVote(ballot);
      if (vote) votes.push(vote);
    }
    const quorumWeight = votes.reduce((sum, v) => sum + v.weight, 0);
    const required = this.requiredWeight();
    if (quorumWeight < required) {
      throw new QuorumNotReachedError(quorumWeight, required, this.topology);
    }
    const evidenceHash = sha256Hex(
      votes
        .map((v) => v.signature)
        .sort()
        .join(":") + `|${this.epoch}|${decision}|${this.topology}`,
    );
    const result: ArbitrationResult = {
      epoch: this.epoch,
      decision,
      topology: this.topology,
      quorumWeight,
      requiredWeight: required,
      votes,
      evidenceHash,
      decidedAt: new Date().toISOString(),
    };
    this.decisionLog.push(result);
    return result;
  }

  /**
   * 独立验证一份仲裁证据：
   * 1. 逐票验证 Ed25519 签名与选票内容一致；
   * 2. 权重合计达到该结果记录的门限；
   * 3. 证据摘要可复算。
   */
  verifyEvidence(result: ArbitrationResult): boolean {
    for (const vote of result.votes) {
      const ballot: Ballot = {
        epoch: result.epoch,
        decision: result.decision,
        topology: result.topology,
        voter: vote.regionId,
      };
      if (!verifyVote(ballot, vote)) return false;
    }
    const weight = result.votes.reduce((sum, v) => sum + v.weight, 0);
    if (weight < result.requiredWeight) return false;
    const hash = sha256Hex(
      result.votes
        .map((v) => v.signature)
        .sort()
        .join(":") + `|${result.epoch}|${result.decision}|${result.topology}`,
    );
    return hash === result.evidenceHash;
  }

  /**
   * 应用一份仲裁决议到状态机。
   * 纪元围栏：低于当前纪元的决议一律拒绝，保证接管后无状态冲突。
   */
  applyDecision(result: ArbitrationResult): boolean {
    if (result.epoch < this.epoch) return false;
    if (!this.verifyEvidence(result)) return false;
    return true;
  }
}
