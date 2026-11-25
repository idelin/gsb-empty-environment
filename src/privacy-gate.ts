import { Fraction } from "./fraction.ts";

/**
 * 隐私宿主放行器。
 *
 * 承诺：同一组可串通站点在一次会话中拿到的全部公开结果，
 * 对任一秘密取值 s 都满足 posterior(s) <= bound * prior(s)（默认 bound=3）。
 * 全部计算使用精确分数；比值等于 bound 视为合规，严格大于才越界。
 */

export type IntLike = bigint | number | string;
export type Prob = Fraction | readonly [IntLike, IntLike] | bigint | number;

export function toFraction(value: Prob): Fraction {
  if (value instanceof Fraction) return value;
  if (Array.isArray(value)) return new Fraction(value[0], value[1]);
  return new Fraction(value);
}

export interface SecretSpec {
  id: string;
  prior: Prob;
}

export interface ModuleSpec {
  id: string;
  candidates: readonly string[];
  table: Record<string, Record<string, Prob>>;
}

export interface GateConfig {
  secrets: readonly SecretSpec[];
  modules: readonly ModuleSpec[];
  bound?: Fraction;
  rejectionResourceId?: string;
}

export interface ResourceEvent {
  eventId: string;
  moduleId: string;
  resolution: Record<string, string | null>;
  /**
   * 重放（第二投影）时该观察者实际看到的资源；新事件的准入枚举不需要填写。
   * 联合原像只保留解析到该资源的候选；null 表示它看到的是空观测。
   */
  observedResourceId?: string | null;
}

export interface PublicObservation {
  eventId: string;
  moduleId: string;
  resourceId: string | null;
}

export interface WorstPosterior {
  secret: string;
  posterior: Fraction;
  ratio: Fraction;
  evidence: PublicObservation[];
}

export interface OutcomeAssessment {
  resourceId: string | null;
  allowedCandidates: string[];
  posteriors: Record<string, Fraction>;
  ratios: Record<string, Fraction>;
  worstSecret: string;
  worstPosterior: Fraction;
  worstRatio: Fraction;
  impossible: boolean;
  violates: boolean;
}

export interface ProposalResult {
  eventId: string;
  admitted: boolean;
  publicOutput: { resourceId: string; rejection: boolean };
  outcomes: OutcomeAssessment[];
  reason?: string;
}

export interface MergeReport {
  merged: boolean;
  replayed: number;
  appended: number;
  reason?: string;
}

export interface PosteriorReport {
  likelihoods: Record<string, Fraction>;
  posteriors: Record<string, Fraction>;
  ratios: Record<string, Fraction>;
  worst: WorstPosterior;
  impossible: boolean;
  violates: boolean;
}

interface Constraint {
  eventId: string;
  moduleId: string;
  resourceId: string | null;
  allowed: Set<string>;
  resolution: Record<string, string | null>;
}

interface ResolutionGroup {
  resourceId: string | null;
  allowed: Set<string>;
}

const DEFAULT_BOUND = new Fraction(3n);
const DEFAULT_REJECTION = "__host_rejected__";
const EMPTY_RESOURCE = "__empty__";

function resourceKey(resourceId: string | null): string {
  return resourceId === null ? EMPTY_RESOURCE : resourceId;
}

function formatResource(resourceId: string | null): string {
  return resourceId === null ? "空观测" : resourceId;
}

export class PrivacyGate {
  readonly bound: Fraction;
  readonly rejectionResourceId: string;
  private readonly secretIds: string[] = [];
  private readonly priors = new Map<string, Fraction>();
  private readonly modules = new Map<string, ModuleSpec>();
  private readonly constraints: Constraint[] = [];
  private readonly byEventId = new Map<string, number>();
  private readonly log: PublicObservation[] = [];
  private rejectionCount = 0;

  constructor(config: GateConfig) {
    this.bound = config.bound ?? DEFAULT_BOUND;
    this.rejectionResourceId = config.rejectionResourceId ?? DEFAULT_REJECTION;
    if (this.bound.compareTo(Fraction.ONE) < 0) {
      throw new Error("PrivacyGate: bound 必须 >= 1");
    }
    if (config.secrets.length === 0) throw new Error("PrivacyGate: 至少需要一个秘密取值");
    let priorSum = Fraction.ZERO;
    for (const spec of config.secrets) {
      if (this.priors.has(spec.id)) throw new Error(`PrivacyGate: 秘密 id 重复 ${spec.id}`);
      const prior = toFraction(spec.prior);
      if (prior.num <= 0n) throw new Error(`PrivacyGate: 先验必须为正（${spec.id}）`);
      this.priors.set(spec.id, prior);
      this.secretIds.push(spec.id);
      priorSum = priorSum.add(prior);
    }
    if (!priorSum.equals(Fraction.ONE)) {
      throw new Error(`PrivacyGate: 先验求和必须为 1，实际为 ${priorSum}`);
    }
    for (const mod of config.modules) {
      if (this.modules.has(mod.id)) throw new Error(`PrivacyGate: 模块 id 重复 ${mod.id}`);
      this.validateModule(mod);
      this.modules.set(mod.id, mod);
    }
  }

  private validateModule(mod: ModuleSpec): void {
    const candidateSet = new Set<string>();
    for (const candidate of mod.candidates) {
      if (candidateSet.has(candidate)) {
        throw new Error(`PrivacyGate: 模块 ${mod.id} 候选编号重复 ${candidate}`);
      }
      candidateSet.add(candidate);
    }
    for (const secretId of this.secretIds) {
      const row = mod.table[secretId];
      if (!row) throw new Error(`PrivacyGate: 模块 ${mod.id} 缺少秘密 ${secretId} 的概率行`);
      let rowSum = Fraction.ZERO;
      for (const candidate of mod.candidates) {
        const raw = row[candidate];
        if (raw === undefined) {
          throw new Error(`PrivacyGate: 模块 ${mod.id} 缺少 P(${candidate}|${secretId})`);
        }
        const probability = toFraction(raw);
        if (probability.num < 0n) {
          throw new Error(`PrivacyGate: 概率不能为负（${mod.id}/${secretId}/${candidate}）`);
        }
        rowSum = rowSum.add(probability);
      }
      if (!rowSum.equals(Fraction.ONE)) {
        throw new Error(`PrivacyGate: 模块 ${mod.id} 在秘密 ${secretId} 下概率和为 ${rowSum}，应为 1`);
      }
    }
  }

  publicLog(): PublicObservation[] {
    return this.log.map((item) => ({ ...item }));
  }

  recordedEventIds(): string[] {
    return this.constraints.map((constraint) => constraint.eventId);
  }

  rejections(): number {
    return this.rejectionCount;
  }

  posteriors(): Record<string, Fraction> {
    return this.posteriorsUnder(this.constraints).posteriors;
  }

  /** 当前约束下各秘密的精确联合似然（未乘先验）。 */
  likelihoods(): Record<string, Fraction> {
    return this.posteriorsUnder(this.constraints).likelihoods;
  }

  currentWorst(): WorstPosterior {
    return this.posteriorsUnder(this.constraints).worst;
  }

  /**
   * 纯函数预检：枚举新事件每个可能结果的后验，判断是否会超额，不修改宿主状态。
   * 与 {@link propose} 使用同一套核算，供穷举器在克隆上批量评估。
   */
  evaluateNew(event: ResourceEvent): { outcomes: OutcomeAssessment[]; violates: boolean } {
    this.checkEvent(event);
    const groups = this.resolutionGroups(event);
    const outcomes = groups.map((group) =>
      this.assess(event.moduleId, group.resourceId, group.allowed, event.resolution),
    );
    return { outcomes, violates: outcomes.some((outcome) => outcome.violates) };
  }

  /**
   * 准入入口。新事件：准入前枚举该模块每个可能的公开结果，
   * 任一结果在任一秘密下超额 => 整个事件对所有秘密发出同一固定拒绝结果，
   * 不写入任何约束、不产生差异化错误码。
   * 已存在 eventId：按同一次抽样的另一投影做联合原像交集（重放）。
   */
  propose(event: ResourceEvent): ProposalResult {
    this.checkEvent(event);
    const existingIndex = this.byEventId.get(event.eventId);
    if (existingIndex !== undefined) return this.proposeReplay(event, existingIndex);
    return this.proposeNew(event);
  }

  private proposeNew(event: ResourceEvent): ProposalResult {
    const groups = this.resolutionGroups(event);
    const outcomes = groups.map((group) =>
      this.assess(event.moduleId, group.resourceId, group.allowed),
    );
    const offending = outcomes.find((outcome) => outcome.violates);
    if (offending) {
      this.rejectionCount += 1;
      return {
        eventId: event.eventId,
        admitted: false,
        publicOutput: { resourceId: this.rejectionResourceId, rejection: true },
        outcomes,
        reason: `结果 ${formatResource(offending.resourceId)} 在秘密 ${offending.worstSecret} 下后验/先验=${offending.worstRatio} 超过 ${this.bound}`,
      };
    }
    const observed = event.observedResourceId === undefined ? null : event.observedResourceId;
    const chosen = groups.find((group) => group.resourceId === observed);
    if (!chosen) {
      throw new Error(
        `PrivacyGate: observedResourceId ${formatResource(observed)} 不在事件 ${event.eventId} 的解析结果中`,
      );
    }
    this.appendConstraint(event, chosen.resourceId, chosen.allowed);
    return {
      eventId: event.eventId,
      admitted: true,
      publicOutput: { resourceId: resourceKey(chosen.resourceId), rejection: false },
      outcomes,
    };
  }

  private proposeReplay(event: ResourceEvent, existingIndex: number): ProposalResult {
    const existing = this.constraints[existingIndex];
    if (existing.moduleId !== event.moduleId) {
      throw new Error(
        `PrivacyGate: 重放事件 ${event.eventId} 的模块不一致（${existing.moduleId} vs ${event.moduleId}）`,
      );
    }
    const projected = this.preimage(event);
    const jointPreimage = [...existing.allowed].filter((candidate) =>
      projected.allowed.has(candidate),
    );
    const joint = new Set(jointPreimage);
    const mergedConstraints = this.constraints.map((constraint, index) =>
      index === existingIndex ? { ...constraint, allowed: joint } : constraint,
    );
    const report = this.posteriorsUnder(mergedConstraints);
    const assessment: OutcomeAssessment = {
      resourceId: existing.resourceId,
      allowedCandidates: jointPreimage,
      posteriors: report.posteriors,
      ratios: report.ratios,
      worstSecret: report.worst.secret,
      worstPosterior: report.worst.posterior,
      worstRatio: report.worst.ratio,
      impossible: report.impossible,
      violates: report.violates,
    };
    if (assessment.violates || assessment.impossible) {
      this.rejectionCount += 1;
      return {
        eventId: event.eventId,
        admitted: false,
        publicOutput: { resourceId: this.rejectionResourceId, rejection: true },
        outcomes: [assessment],
        reason: assessment.impossible
          ? `重放事件 ${event.eventId} 的联合原像为空：同一次抽样不可能同时满足两个投影`
          : `重放事件 ${event.eventId} 收紧联合原像后在秘密 ${assessment.worstSecret} 下超额`,
      };
    }
    this.constraints[existingIndex] = mergedConstraints[existingIndex];
    return {
      eventId: event.eventId,
      admitted: true,
      publicOutput: { resourceId: resourceKey(existing.resourceId), rejection: false },
      outcomes: [assessment],
    };
  }

  /**
   * 合并另一个观察者：共同 eventId 去重并做联合原像（同一次抽样，不是两次独立抽样），
   * 其余事件按对方原始顺序追加。任一步骤不合规则整体不生效。
   */
  merge(other: PrivacyGate): MergeReport {
    this.assertSameConfig(other);
    const scratch = this.clone();
    let replayed = 0;
    let appended = 0;
    for (const event of other.constraints) {
      if (scratch.byEventId.has(event.eventId)) {
        const replay: ResourceEvent = {
          eventId: event.eventId,
          moduleId: event.moduleId,
          observedResourceId: event.resourceId,
          resolution: event.resolution,
        };
        const result = scratch.propose(replay);
        if (!result.admitted) return { merged: false, replayed, appended, reason: result.reason };
        replayed += 1;
      } else {
        scratch.constraints.push({ ...event, allowed: new Set(event.allowed) });
        scratch.byEventId.set(event.eventId, scratch.constraints.length - 1);
        scratch.log.push({
          eventId: event.eventId,
          moduleId: event.moduleId,
          resourceId: event.resourceId,
        });
        appended += 1;
      }
    }
    const finalCheck = scratch.currentWorst();
    if (finalCheck.ratio.compareTo(this.bound) > 0) {
      return {
        merged: false,
        replayed,
        appended,
        reason: `合并后秘密 ${finalCheck.secret} 的后验/先验=${finalCheck.ratio} 超过 ${this.bound}`,
      };
    }
    this.replaceState(scratch);
    return { merged: true, replayed, appended };
  }

  /** 纯函数：假设再观察到某模块的一个公开资源，后验会变成什么。不修改宿主状态。 */
  hypothetical(event: ResourceEvent, resourceId: string | null): PosteriorReport {
    this.checkEvent(event);
    const groups = this.resolutionGroups(event);
    const group = groups.find((candidate) => candidate.resourceId === resourceId);
    if (!group) {
      throw new Error(
        `PrivacyGate: 资源 ${formatResource(resourceId)} 不在事件 ${event.eventId} 的解析结果中`,
      );
    }
    const next = [...this.constraints, this.buildConstraint(event, group.resourceId, group.allowed)];
    return this.posteriorsUnder(next);
  }

  clone(): PrivacyGate {
    const copy = Object.create(PrivacyGate.prototype) as PrivacyGate;
    copy.installFrom(this);
    return copy;
  }

  private installFrom(source: PrivacyGate): void {
    Object.assign(this, {
      bound: source.bound,
      rejectionResourceId: source.rejectionResourceId,
      secretIds: [...source.secretIds],
      priors: new Map(source.priors),
      modules: new Map(source.modules),
      constraints: source.constraints.map((constraint) => ({
        ...constraint,
        allowed: new Set(constraint.allowed),
        resolution: { ...constraint.resolution },
      })),
      byEventId: new Map(source.byEventId),
      log: source.log.map((item) => ({ ...item })),
      rejectionCount: source.rejectionCount,
    });
  }

  private replaceState(source: PrivacyGate): void {
    this.installFrom(source);
  }

  private assertSameConfig(other: PrivacyGate): void {
    if (other.bound.compareTo(this.bound) !== 0) {
      throw new Error("PrivacyGate.merge: 两个宿主的 bound 不一致");
    }
    if (other.rejectionResourceId !== this.rejectionResourceId) {
      throw new Error("PrivacyGate.merge: 两个宿主的拒绝资源标识不一致");
    }
    for (const secretId of this.secretIds) {
      const otherPrior = other.priors.get(secretId);
      if (!otherPrior || !otherPrior.equals(this.priors.get(secretId)!)) {
        throw new Error(`PrivacyGate.merge: 秘密 ${secretId} 的先验不一致`);
      }
    }
    for (const [moduleId, moduleSpec] of this.modules) {
      const otherModule = other.modules.get(moduleId);
      if (!otherModule) throw new Error(`PrivacyGate.merge: 缺少模块 ${moduleId}`);
      for (const secretId of this.secretIds) {
        for (const candidate of moduleSpec.candidates) {
          const lhs = toFraction(moduleSpec.table[secretId][candidate]);
          const rhs = toFraction(otherModule.table[secretId][candidate]);
          if (!lhs.equals(rhs)) {
            throw new Error(
              `PrivacyGate.merge: 模块 ${moduleId} 的概率表不一致（${secretId}/${candidate}）`,
            );
          }
        }
      }
    }
  }

  private checkEvent(event: ResourceEvent): void {
    const moduleSpec = this.modules.get(event.moduleId);
    if (!moduleSpec) throw new Error(`PrivacyGate: 未知模块 ${event.moduleId}`);
    for (const candidate of Object.keys(event.resolution)) {
      if (!moduleSpec.candidates.includes(candidate)) {
        throw new Error(`PrivacyGate: 模块 ${event.moduleId} 不存在候选 ${candidate}`);
      }
    }
  }

  /** 把候选编号按解析到的公开资源分桶；同资源的编号对观察者不可区分。 */
  private resolutionGroups(event: ResourceEvent): ResolutionGroup[] {
    const moduleSpec = this.modules.get(event.moduleId)!;
    const order: (string | null)[] = [];
    const buckets = new Map<string | null, Set<string>>();
    for (const candidate of moduleSpec.candidates) {
      const resourceId = candidate in event.resolution ? event.resolution[candidate] : null;
      let bucket = buckets.get(resourceId);
      if (!bucket) {
        bucket = new Set();
        buckets.set(resourceId, bucket);
        order.push(resourceId);
      }
      bucket.add(candidate);
    }
    return order.map((resourceId) => ({
      resourceId,
      allowed: buckets.get(resourceId)!,
    }));
  }

  /** 单个投影下，与可见资源相容的候选编号原像。 */
  private preimage(event: ResourceEvent): ResolutionGroup {
    const moduleSpec = this.modules.get(event.moduleId)!;
    const allowed = new Set<string>();
    const resourceId = event.observedResourceId === undefined ? null : event.observedResourceId;
    for (const [candidate, value] of Object.entries(event.resolution)) {
      if (moduleSpec.candidates.includes(candidate) && value === resourceId) {
        allowed.add(candidate);
      }
    }
    return { resourceId, allowed };
  }

  private buildConstraint(
    event: ResourceEvent,
    resourceId: string | null,
    allowed: Set<string>,
  ): Constraint {
    return {
      eventId: event.eventId,
      moduleId: event.moduleId,
      resourceId,
      allowed: new Set(allowed),
      resolution: { ...event.resolution },
    };
  }

  private appendConstraint(
    event: ResourceEvent,
    resourceId: string | null,
    allowed: Set<string>,
  ): void {
    this.constraints.push(this.buildConstraint(event, resourceId, allowed));
    this.byEventId.set(event.eventId, this.constraints.length - 1);
    this.log.push({ eventId: event.eventId, moduleId: event.moduleId, resourceId });
  }

  private assess(
    moduleId: string,
    resourceId: string | null,
    allowed: Set<string>,
    resolution: Record<string, string | null>,
  ): OutcomeAssessment {
    const moduleSpec = this.modules.get(moduleId)!;
    const trial: Constraint = {
      eventId: `__trial_${this.constraints.length}`,
      moduleId,
      resourceId,
      allowed: new Set(allowed),
      resolution,
    };
    const report = this.posteriorsUnder([...this.constraints, trial]);
    return {
      resourceId,
      allowedCandidates: moduleSpec.candidates.filter((candidate) => allowed.has(candidate)),
      posteriors: report.posteriors,
      ratios: report.ratios,
      worstSecret: report.worst.secret,
      worstPosterior: report.worst.posterior,
      worstRatio: report.worst.ratio,
      impossible: report.impossible,
      violates: report.violates,
    };
  }

  /**
   * 给定一组约束（不同抽样在秘密条件下独立），计算精确联合似然与贝叶斯后验。
   * 同一 eventId 的多个投影在调用前已被收紧为一个交集约束，因此不会被重复计数。
   */
  private posteriorsUnder(candidateConstraints: Constraint[]): PosteriorReport {
    const likelihoods: Record<string, Fraction> = {};
    const weights: Record<string, Fraction> = {};
    let normalizer = Fraction.ZERO;
    for (const secretId of this.secretIds) {
      let likelihood = Fraction.ONE;
      for (const constraint of candidateConstraints) {
        const moduleSpec = this.modules.get(constraint.moduleId)!;
        let factor = Fraction.ZERO;
        for (const candidate of constraint.allowed) {
          factor = factor.add(toFraction(moduleSpec.table[secretId][candidate]));
        }
        likelihood = likelihood.mul(factor);
      }
      likelihoods[secretId] = likelihood;
      const weight = this.priors.get(secretId)!.mul(likelihood);
      weights[secretId] = weight;
      normalizer = normalizer.add(weight);
    }
    const posteriors: Record<string, Fraction> = {};
    const ratios: Record<string, Fraction> = {};
    const impossible = normalizer.isZero();
    for (const secretId of this.secretIds) {
      const posterior = impossible
        ? Fraction.ZERO
        : weights[secretId].div(normalizer);
      posteriors[secretId] = posterior;
      ratios[secretId] = impossible
        ? Fraction.ZERO
        : likelihoods[secretId].div(normalizer);
    }
    let worstSecret = this.secretIds[0];
    for (const secretId of this.secretIds.slice(1)) {
      if (posteriors[secretId].compareTo(posteriors[worstSecret]) > 0) worstSecret = secretId;
    }
    const evidence = candidateConstraints
      .filter((constraint) => !constraint.eventId.startsWith("__trial_"))
      .map((constraint) => ({
        eventId: constraint.eventId,
        moduleId: constraint.moduleId,
        resourceId: constraint.resourceId,
      }));
    const worst: WorstPosterior = {
      secret: worstSecret,
      posterior: posteriors[worstSecret],
      ratio: ratios[worstSecret],
      evidence,
    };
    return {
      likelihoods,
      posteriors,
      ratios,
      worst,
      impossible,
      violates: !impossible && worst.ratio.compareTo(this.bound) > 0,
    };
  }
}
