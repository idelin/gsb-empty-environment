import {
  buildPrivacyReport,
  createEvaluationContext,
  evaluatePrivacy,
  evaluateTraceWithContext,
  reviewAdaptiveStrategy,
  type AdaptiveScheduler,
  type EvaluationContext,
} from './evaluate.ts';
import { resolvePolicy } from './policy.ts';
import { FIXED_REJECTION } from './fixed.ts';
import type {
  GroupId,
  ObservationInput,
  PrivacyReport,
  ReleaseResult,
  SignatureVerifier,
  SignedWorkModule,
  JointPreimageTableInput,
} from './types.ts';

export { FIXED_REJECTION, fixedRejection } from './fixed.ts';

export interface BillingSummary {
  readonly eventIds: readonly string[];
  readonly sampleIds: readonly string[];
  readonly eventCount: number;
  readonly sampleCount: number;
}

export interface GroupSnapshot {
  readonly id: GroupId;
  readonly observations: readonly ObservationInput[];
  readonly billing: BillingSummary;
}

export interface SessionSnapshot {
  readonly groups: readonly GroupSnapshot[];
}

interface GroupState {
  id: GroupId;
  observations: ObservationInput[];
}

export interface PrivacySessionOptions {
  readonly policy: ConstructorParameters<typeof resolvePolicy>[0];
  readonly signatureVerifier?: SignatureVerifier;
  readonly modules?: readonly SignedWorkModule[] | Readonly<Record<string, SignedWorkModule>>;
  readonly jointTables?:
    | Readonly<Record<string, JointPreimageTableInput>>
    | readonly JointPreimageTableInput[];
  readonly strategy?: AdaptiveScheduler;
}

export interface MergeGroupsOptions {
  readonly mergedId?: GroupId;
  readonly keepOriginals?: boolean;
  readonly strategy?: AdaptiveScheduler;
  readonly maxDepth?: number;
  readonly maxTraces?: number;
}

export interface MergePreview {
  readonly mergedId: GroupId;
  readonly observations: readonly ObservationInput[];
  readonly billing: BillingSummary;
  readonly report: PrivacyReport;
  readonly exhaustiveReport?: PrivacyReport;
}

function moduleList(
  modules: PrivacySessionOptions['modules'],
): SignedWorkModule[] {
  if (!modules) return [];
  return Array.isArray(modules) ? [...modules] : Object.values(modules);
}

export function billingForObservations(observations: readonly ObservationInput[]): BillingSummary {
  const events = new Map<string, string>();
  for (const observation of observations) {
    if (!events.has(observation.eventId)) {
      events.set(observation.eventId, observation.sampleId ?? observation.eventId);
    }
  }
  const eventIds = [...events.keys()];
  const sampleIds = [...new Set(events.values())];
  return Object.freeze({
    eventIds: Object.freeze(eventIds),
    sampleIds: Object.freeze(sampleIds),
    eventCount: eventIds.length,
    sampleCount: sampleIds.length,
  });
}

export class PrivacySession {
  readonly policy: ReturnType<typeof resolvePolicy>;
  private readonly verifier?: SignatureVerifier;
  private readonly jointTables?: PrivacySessionOptions['jointTables'];
  private readonly strategy?: AdaptiveScheduler;
  private readonly rawModules = new Map<string, SignedWorkModule>();
  private readonly groups = new Map<GroupId, GroupState>();
  private context: EvaluationContext;

  constructor(options: PrivacySessionOptions) {
    this.policy = resolvePolicy(options.policy);
    this.verifier = options.signatureVerifier;
    this.jointTables = options.jointTables;
    this.strategy = options.strategy;
    this.context = this.rebuildContext();
    for (const module of moduleList(options.modules)) {
      this.importModule(module);
    }
    this.context = this.rebuildContext();
  }

  private rebuildContext(): EvaluationContext {
    return createEvaluationContext({
      policy: this.policy,
      modules: [...this.rawModules.values()],
      jointTables: this.jointTables,
      signatureVerifier: this.verifier,
    });
  }

  get evaluationContext(): EvaluationContext {
    return this.context;
  }

  importModule(module: SignedWorkModule): ReleaseResult<{ moduleId: string }> {
    const probe = createEvaluationContext({
      policy: this.policy,
      modules: [module],
      jointTables: this.jointTables,
      signatureVerifier: this.verifier,
    });
    if (!probe.trusted || probe.modules.size !== 1) return FIXED_REJECTION;
    this.rawModules.set(module.id, module);
    this.context = this.rebuildContext();
    return Object.freeze({
      ok: true,
      allowed: true,
      decision: 'allow',
      code: 'RELEASE_ALLOWED',
      data: Object.freeze({ moduleId: module.id }),
    });
  }

  diagnoseModule(module: SignedWorkModule): PrivacyReport {
    return evaluatePrivacy({
      policy: this.policy,
      modules: [module],
      jointTables: this.jointTables,
      signatureVerifier: this.verifier,
    });
  }

  createGroup(id: GroupId): ReleaseResult<{ groupId: GroupId }> {
    if (!id || this.groups.has(id)) return FIXED_REJECTION;
    this.groups.set(id, { id, observations: [] });
    return Object.freeze({
      ok: true,
      allowed: true,
      decision: 'allow',
      code: 'RELEASE_ALLOWED',
      data: Object.freeze({ groupId: id }),
    });
  }

  hasGroup(id: GroupId): boolean {
    return this.groups.has(id);
  }

  snapshot(): SessionSnapshot {
    return Object.freeze({
      groups: Object.freeze(
        [...this.groups.values()].map((group) => {
          const observations = group.observations.map((observation) => Object.freeze({ ...observation }));
          return Object.freeze({
            id: group.id,
            observations: Object.freeze(observations),
            billing: billingForObservations(observations),
          });
        }),
      ),
    });
  }

  getGroup(id: GroupId): GroupSnapshot | null {
    const group = this.groups.get(id);
    if (!group) return null;
    const observations = group.observations.map((observation) => Object.freeze({ ...observation }));
    return Object.freeze({
      id: group.id,
      observations: Object.freeze(observations),
      billing: billingForObservations(observations),
    });
  }

  reviewObservations(observations: readonly ObservationInput[]): PrivacyReport {
    const trace = evaluateTraceWithContext(this.context, observations, 'review');
    return buildPrivacyReport(this.context, [trace]);
  }

  reviewGroup(id: GroupId): PrivacyReport | null {
    const group = this.groups.get(id);
    if (!group) return null;
    return this.reviewObservations(group.observations);
  }

  reviewStrategy(strategy: AdaptiveScheduler, options?: { maxDepth?: number; maxTraces?: number }): PrivacyReport {
    return reviewAdaptiveStrategy({
      policy: this.policy,
      modules: [...this.rawModules.values()],
      jointTables: this.jointTables,
      signatureVerifier: this.verifier,
      strategy,
      ...(options?.maxDepth !== undefined ? { maxDepth: options.maxDepth } : {}),
      ...(options?.maxTraces !== undefined ? { maxTraces: options.maxTraces } : {}),
    });
  }

  authorizeCall(
    groupId: GroupId,
    call: { moduleId: string; eventId: string; sampleId: string; projection?: string; jointTableId?: string },
  ): ReleaseResult<{ traces: number }> {
    const group = this.groups.get(groupId);
    const module = this.context.modules.get(call.moduleId);
    if (!group || !module || !this.context.trusted) return FIXED_REJECTION;

    const possibleValues = new Set<string>();
    for (const secret of this.policy.secrets) {
      for (const [candidate, probability] of Object.entries(module.tables[secret])) {
        if (probability.isZero()) continue;
        possibleValues.add(
          candidate === '' ? '∅' : module.resourceOf[candidate] ?? candidate,
        );
      }
    }

    for (const publicValue of possibleValues) {
      const empty = publicValue === '∅';
      const candidateObservation: ObservationInput = {
        eventId: call.eventId,
        sampleId: call.sampleId,
        moduleId: call.moduleId,
        projection: call.projection ?? call.moduleId,
        empty,
        ...(empty ? {} : { resourceId: publicValue }),
        ...(call.jointTableId ? { jointTableId: call.jointTableId } : {}),
      };
      const report = this.reviewObservations([...group.observations, candidateObservation]);
      if (!report.allowed) return FIXED_REJECTION;
    }

    return Object.freeze({
      ok: true,
      allowed: true,
      decision: 'allow',
      code: 'RELEASE_ALLOWED',
      data: Object.freeze({ traces: possibleValues.size }),
    });
  }

  recordObservation(groupId: GroupId, observation: ObservationInput): ReleaseResult<BillingSummary> {
    const group = this.groups.get(groupId);
    if (!group || !this.context.trusted) return FIXED_REJECTION;

    const candidateObservations = [...group.observations, observation];
    const report = this.reviewObservations(candidateObservations);
    if (!report.allowed) return FIXED_REJECTION;

    const snapshot = this.snapshot();
    try {
      group.observations = candidateObservations;
      return Object.freeze({
        ok: true,
        allowed: true,
        decision: 'allow',
        code: 'RELEASE_ALLOWED',
        data: billingForObservations(group.observations),
      });
    } catch {
      this.restoreSnapshot(snapshot);
      return FIXED_REJECTION;
    }
  }

  previewMerge(groupA: GroupId, groupB: GroupId, options: MergeGroupsOptions = {}): MergePreview | null {
    const left = this.groups.get(groupA);
    const right = this.groups.get(groupB);
    if (!left || !right) return null;
    const mergedId = options.mergedId ?? `${groupA}__${groupB}`;
    const observations = [...left.observations, ...right.observations];
    const report = this.reviewObservations(observations);
    let exhaustiveReport: PrivacyReport | undefined;
    const strategy = options.strategy ?? this.strategy;
    if (strategy) {
      exhaustiveReport = this.reviewStrategy(strategy, {
        ...(options.maxDepth !== undefined ? { maxDepth: options.maxDepth } : {}),
        ...(options.maxTraces !== undefined ? { maxTraces: options.maxTraces } : {}),
      });
    }
    return Object.freeze({
      mergedId,
      observations: Object.freeze(observations),
      billing: billingForObservations(observations),
      report,
      ...(exhaustiveReport ? { exhaustiveReport } : {}),
    });
  }

  mergeGroups(groupA: GroupId, groupB: GroupId, options: MergeGroupsOptions = {}): ReleaseResult<GroupSnapshot> {
    const preview = this.previewMerge(groupA, groupB, options);
    if (!preview || !preview.report.allowed || (preview.exhaustiveReport && !preview.exhaustiveReport.allowed)) {
      return FIXED_REJECTION;
    }

    const snapshot = this.snapshot();
    try {
      const merged: GroupState = { id: preview.mergedId, observations: [...preview.observations] };
      this.groups.set(preview.mergedId, merged);
      if (!options.keepOriginals) {
        if (preview.mergedId !== groupA) this.groups.delete(groupA);
        if (preview.mergedId !== groupB) this.groups.delete(groupB);
      }
      return Object.freeze({
        ok: true,
        allowed: true,
        decision: 'allow',
        code: 'RELEASE_ALLOWED',
        data: this.getGroup(preview.mergedId) ?? undefined,
      }) as ReleaseResult<GroupSnapshot>;
    } catch {
      this.restoreSnapshot(snapshot);
      return FIXED_REJECTION;
    }
  }

  private restoreSnapshot(snapshot: SessionSnapshot): void {
    this.groups.clear();
    for (const group of snapshot.groups) {
      this.groups.set(group.id, { id: group.id, observations: [...group.observations] });
    }
  }
}

export const PrivacyReleaseGate = PrivacySession;
export const PrivacyGate = PrivacySession;
export const PrivacyHost = PrivacySession;

export function createPrivacyGate(options: PrivacySessionOptions): PrivacySession {
  return new PrivacySession(options);
}

export function mergeObserverGroups(
  session: PrivacySession,
  groupA: GroupId,
  groupB: GroupId,
  options?: MergeGroupsOptions,
): ReleaseResult<GroupSnapshot> {
  return session.mergeGroups(groupA, groupB, options);
}
