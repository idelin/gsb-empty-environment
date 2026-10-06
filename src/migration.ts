import { WorkflowEngine } from "./engine";
import { replay, ReplayResult } from "./replay";
import { UnmappedStateError, WorkflowError } from "./errors";
import type {
  EventEnvelope,
  GraphVersion,
  InstanceId,
  MigrationPayload,
  StateMapping,
  StateName,
} from "./types";

export interface MigrationPlanEntry {
  readonly instanceId: InstanceId;
  readonly fromState: StateName;
  /** Absent when the instance is terminal and left untouched. */
  readonly toState?: StateName;
  readonly skipped: boolean;
}

export interface MigrationResult {
  readonly fromVersion: GraphVersion;
  readonly toVersion: GraphVersion;
  readonly migrated: readonly MigrationPlanEntry[];
  readonly skipped: readonly MigrationPlanEntry[];
  readonly events: readonly EventEnvelope<MigrationPayload>[];
}

export interface UpgradeAudit {
  readonly instanceId: InstanceId;
  /** Replayed just before the migration event, against the old graph. */
  readonly before: ReplayResult;
  /** Replayed over the full log, against the new graph. */
  readonly after: ReplayResult;
  readonly migrationSeq: number;
}

/**
 * Two-phase batch migration.
 *
 * Phase 1 plans every instance: each must sit on `fromVersion`, and every
 * active (non-terminal) state must appear in `mapping`. Any violation
 * aborts the whole batch before a single event is written.
 *
 * Phase 2 appends one migration event per instance. Because migration
 * events live in the same per-instance sequence as ordinary transition
 * events, the upgrade is atomically ordered with respect to any new
 * command: whatever is appended later wins a higher seq, and commands
 * still carrying the old version are rejected on dispatch.
 *
 * Several old states may map onto one new state (merge). The historical
 * transition events are never rewritten, so the full pre-merge history
 * remains replayable.
 */
export function migrateBatch(
  engine: WorkflowEngine,
  instanceIds: readonly InstanceId[],
  fromVersion: GraphVersion,
  toVersion: GraphVersion,
  mapping: StateMapping,
): MigrationResult {
  const newGraph = engine.registry.get(toVersion);
  engine.registry.get(fromVersion);
  for (const [from, to] of Object.entries(mapping)) {
    if (!newGraph.states.has(to)) {
      throw new WorkflowError(
        `Mapping "${from}" -> "${to}" targets a state unknown to graph "${toVersion}".`,
      );
    }
  }

  const plan: MigrationPlanEntry[] = [];
  const unmapped: { instanceId: InstanceId; state: StateName }[] = [];
  for (const instanceId of instanceIds) {
    const current = engine.inspect(instanceId);
    if (current.version !== fromVersion) {
      throw new WorkflowError(
        `Instance "${instanceId}" is on version "${current.version}", ` +
          `expected "${fromVersion}".`,
      );
    }
    if (current.terminal) {
      plan.push({
        instanceId,
        fromState: current.state,
        skipped: true,
      });
      continue;
    }
    const toState = mapping[current.state];
    if (toState === undefined) {
      unmapped.push({ instanceId, state: current.state });
      continue;
    }
    plan.push({
      instanceId,
      fromState: current.state,
      toState,
      skipped: false,
    });
  }
  if (unmapped.length > 0) {
    throw new UnmappedStateError(unmapped);
  }

  const events: EventEnvelope<MigrationPayload>[] = [];
  for (const entry of plan) {
    if (entry.skipped || entry.toState === undefined) {
      continue;
    }
    events.push(
      engine.store.append(entry.instanceId, toVersion, {
        type: "migration",
        fromVersion,
        toVersion,
        fromState: entry.fromState,
        toState: entry.toState,
        mapping,
      }),
    );
  }

  return {
    fromVersion,
    toVersion,
    migrated: plan.filter((entry) => !entry.skipped),
    skipped: plan.filter((entry) => entry.skipped),
    events,
  };
}

/**
 * Verify an upgrade by replaying history twice: up to (but excluding) the
 * migration event under the old graph, and the full log under the new one.
 * Callers can diff `before.availableActions` against `after.availableActions`
 * to confirm the mapping preserves (or intentionally changes) executability.
 */
export function auditUpgrade(
  engine: WorkflowEngine,
  instanceId: InstanceId,
): UpgradeAudit {
  const log = engine.store.load(instanceId);
  const migration = [...log].reverse().find(
    (envelope) => envelope.payload.type === "migration",
  );
  if (!migration) {
    throw new WorkflowError(
      `Instance "${instanceId}" has no migration event to audit.`,
    );
  }
  return {
    instanceId,
    before: replay(engine.registry, log, migration.seq - 1),
    after: replay(engine.registry, log),
    migrationSeq: migration.seq,
  };
}
