import { CorruptHistoryError } from "./errors.ts";
import { actionsFrom } from "./graph.ts";
import type { GraphVersion, InstanceEvent } from "./types.ts";

export type GraphResolver = (version: number) => GraphVersion;

export interface ReplayState {
  readonly graphVersion: number;
  readonly state: string;
}

export function replay(log: readonly InstanceEvent[], resolveGraph: GraphResolver): ReplayState {
  if (log.length === 0) {
    throw new CorruptHistoryError("Event log is empty; an instance log must start with a created event.");
  }
  let graphVersion = -1;
  let state = "";
  let expectedSeq = 1;
  for (const event of log) {
    if (event.seq !== expectedSeq) {
      throw new CorruptHistoryError(`Sequence gap: expected seq ${expectedSeq}, got ${event.seq}.`);
    }
    expectedSeq += 1;
    switch (event.kind) {
      case "created": {
        if (event.seq !== 1) {
          throw new CorruptHistoryError(`The created event must be first, found at seq ${event.seq}.`);
        }
        resolveGraph(event.graphVersion);
        graphVersion = event.graphVersion;
        state = event.state;
        break;
      }
      case "transition": {
        if (event.graphVersion !== graphVersion) {
          throw new CorruptHistoryError(
            `Transition "${event.event}" at seq ${event.seq} is stamped with graph v${event.graphVersion} ` +
              `but the instance was on v${graphVersion}.`,
          );
        }
        if (event.from !== state) {
          throw new CorruptHistoryError(
            `Transition "${event.event}" at seq ${event.seq} starts from "${event.from}" ` +
              `but the replayed state is "${state}".`,
          );
        }
        const graph = resolveGraph(event.graphVersion);
        const legal = graph.transitions.some(
          (t) => t.event === event.event && t.to === event.to && t.from.includes(event.from),
        );
        if (!legal) {
          throw new CorruptHistoryError(
            `Transition "${event.event}" from "${event.from}" to "${event.to}" at seq ${event.seq} ` +
              `is not legal under graph v${event.graphVersion}.`,
          );
        }
        state = event.to;
        break;
      }
      case "migrated": {
        if (event.fromVersion !== graphVersion) {
          throw new CorruptHistoryError(
            `Migration at seq ${event.seq} claims fromVersion ${event.fromVersion} ` +
              `but the instance was on v${graphVersion}.`,
          );
        }
        if (event.stateBefore !== state) {
          throw new CorruptHistoryError(
            `Migration at seq ${event.seq} claims stateBefore "${event.stateBefore}" ` +
              `but the replayed state is "${state}".`,
          );
        }
        const target = resolveGraph(event.toVersion);
        const mapped = event.stateMapping[state];
        if (mapped === undefined || mapped !== event.stateAfter) {
          throw new CorruptHistoryError(
            `Migration at seq ${event.seq} maps "${state}" to "${event.stateAfter}" ` +
              `but the recorded mapping yields "${mapped ?? "<unmapped>"}".`,
          );
        }
        if (!target.states.includes(event.stateAfter)) {
          throw new CorruptHistoryError(
            `Migration at seq ${event.seq} lands in "${event.stateAfter}" ` +
              `which is not a state of graph v${event.toVersion}.`,
          );
        }
        graphVersion = event.toVersion;
        state = event.stateAfter;
        break;
      }
    }
  }
  return { graphVersion, state };
}

export interface AuditEntry {
  readonly seq: number;
  readonly kind: InstanceEvent["kind"];
  readonly graphVersion: number;
  readonly state: string;
  readonly availableActions: readonly string[];
}

export interface MigrationCheck {
  readonly seq: number;
  readonly fromVersion: number;
  readonly toVersion: number;
  readonly stateBefore: string;
  readonly stateAfter: string;
  readonly actionsBefore: readonly string[];
  readonly actionsAfter: readonly string[];
  readonly mergedFrom: readonly string[];
}

export interface AuditReport {
  readonly entries: readonly AuditEntry[];
  readonly migrations: readonly MigrationCheck[];
  readonly final: ReplayState;
}

export function auditLog(log: readonly InstanceEvent[], resolveGraph: GraphResolver): AuditReport {
  const entries: AuditEntry[] = [];
  const migrations: MigrationCheck[] = [];
  let graphVersion = -1;
  let state = "";
  let expectedSeq = 1;
  for (const event of log) {
    if (event.seq !== expectedSeq) {
      throw new CorruptHistoryError(`Sequence gap: expected seq ${expectedSeq}, got ${event.seq}.`);
    }
    expectedSeq += 1;
    switch (event.kind) {
      case "created": {
        if (event.seq !== 1) {
          throw new CorruptHistoryError(`The created event must be first, found at seq ${event.seq}.`);
        }
        resolveGraph(event.graphVersion);
        graphVersion = event.graphVersion;
        state = event.state;
        break;
      }
      case "transition": {
        if (event.graphVersion !== graphVersion) {
          throw new CorruptHistoryError(
            `Transition "${event.event}" at seq ${event.seq} is stamped with graph v${event.graphVersion} ` +
              `but the instance was on v${graphVersion}.`,
          );
        }
        if (event.from !== state) {
          throw new CorruptHistoryError(
            `Transition "${event.event}" at seq ${event.seq} starts from "${event.from}" ` +
              `but the replayed state is "${state}".`,
          );
        }
        const graph = resolveGraph(event.graphVersion);
        const legal = graph.transitions.some(
          (t) => t.event === event.event && t.to === event.to && t.from.includes(event.from),
        );
        if (!legal) {
          throw new CorruptHistoryError(
            `Transition "${event.event}" from "${event.from}" to "${event.to}" at seq ${event.seq} ` +
              `is not legal under graph v${event.graphVersion}.`,
          );
        }
        state = event.to;
        break;
      }
      case "migrated": {
        if (event.fromVersion !== graphVersion) {
          throw new CorruptHistoryError(
            `Migration at seq ${event.seq} claims fromVersion ${event.fromVersion} ` +
              `but the instance was on v${graphVersion}.`,
          );
        }
        if (event.stateBefore !== state) {
          throw new CorruptHistoryError(
            `Migration at seq ${event.seq} claims stateBefore "${event.stateBefore}" ` +
              `but the replayed state is "${state}".`,
          );
        }
        const source = resolveGraph(event.fromVersion);
        const target = resolveGraph(event.toVersion);
        const mapped = event.stateMapping[state];
        if (mapped === undefined || mapped !== event.stateAfter) {
          throw new CorruptHistoryError(
            `Migration at seq ${event.seq} maps "${state}" to "${event.stateAfter}" ` +
              `but the recorded mapping yields "${mapped ?? "<unmapped>"}".`,
          );
        }
        if (!target.states.includes(event.stateAfter)) {
          throw new CorruptHistoryError(
            `Migration at seq ${event.seq} lands in "${event.stateAfter}" ` +
              `which is not a state of graph v${event.toVersion}.`,
          );
        }
        migrations.push({
          seq: event.seq,
          fromVersion: event.fromVersion,
          toVersion: event.toVersion,
          stateBefore: state,
          stateAfter: event.stateAfter,
          actionsBefore: actionsFrom(source, state),
          actionsAfter: actionsFrom(target, event.stateAfter),
          mergedFrom: Object.keys(event.stateMapping).filter(
            (oldState) => event.stateMapping[oldState] === event.stateAfter,
          ),
        });
        graphVersion = event.toVersion;
        state = event.stateAfter;
        break;
      }
    }
    entries.push({
      seq: event.seq,
      kind: event.kind,
      graphVersion,
      state,
      availableActions: actionsFrom(resolveGraph(graphVersion), state),
    });
  }
  if (entries.length === 0) {
    throw new CorruptHistoryError("Event log is empty; an instance log must start with a created event.");
  }
  return { entries, migrations, final: { graphVersion, state } };
}
