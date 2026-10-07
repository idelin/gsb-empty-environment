import {
  BatchMigrationError,
  DuplicateInstanceError,
  InvalidTransitionError,
  MissingStateMappingError,
  StaleCommandError,
  UnknownGraphError,
  UnknownInstanceError,
} from "./errors.ts";
import { validateGraph } from "./graph.ts";
import { auditLog, replay } from "./replay.ts";
import type { AuditReport } from "./replay.ts";
import type {
  Command,
  GraphVersion,
  InstanceEvent,
  InstanceSnapshot,
  MigratedEvent,
  MigrationFailure,
  MigrationResult,
} from "./types.ts";

export class WorkflowEngine {
  private readonly graphs = new Map<number, GraphVersion>();
  private readonly instances = new Map<string, InstanceEvent[]>();
  private readonly clock: () => number;

  constructor(clock: () => number = Date.now) {
    this.clock = clock;
  }

  registerGraph(graph: GraphVersion): void {
    if (this.graphs.has(graph.version)) {
      throw new UnknownGraphError(graph.version);
    }
    validateGraph(graph, this.previousGraph(graph.version));
    this.graphs.set(graph.version, graph);
  }

  start(instanceId: string, version?: number): InstanceSnapshot {
    if (this.instances.has(instanceId)) {
      throw new DuplicateInstanceError(instanceId);
    }
    const graph = version === undefined ? this.latestGraph() : this.requireGraph(version);
    const log: InstanceEvent[] = [
      {
        kind: "created",
        seq: 1,
        at: this.clock(),
        graphVersion: graph.version,
        state: graph.initialState,
      },
    ];
    this.instances.set(instanceId, log);
    return this.snapshot(instanceId);
  }

  restore(instanceId: string, log: readonly InstanceEvent[]): InstanceSnapshot {
    if (this.instances.has(instanceId)) {
      throw new DuplicateInstanceError(instanceId);
    }
    replay(log, (version) => this.requireGraph(version));
    this.instances.set(instanceId, log.map((event) => ({ ...event })));
    return this.snapshot(instanceId);
  }

  dispatch(instanceId: string, command: Command): InstanceSnapshot {
    const log = this.requireLog(instanceId);
    const current = replay(log, (version) => this.requireGraph(version));
    if (command.graphVersion !== current.graphVersion) {
      throw new StaleCommandError(instanceId, command.graphVersion, current.graphVersion);
    }
    const graph = this.requireGraph(current.graphVersion);
    const transition = graph.transitions.find(
      (t) => t.event === command.event && t.from.includes(current.state),
    );
    if (transition === undefined) {
      throw new InvalidTransitionError(instanceId, command.event, current.state);
    }
    log.push({
      kind: "transition",
      seq: log.length + 1,
      at: this.clock(),
      graphVersion: graph.version,
      event: command.event,
      from: current.state,
      to: transition.to,
    });
    return this.snapshot(instanceId);
  }

  migrateBatch(instanceIds: readonly string[], toVersion: number): MigrationResult[] {
    const target = this.requireGraph(toVersion);
    const previous = this.previousGraph(toVersion);
    if (previous === undefined || target.stateMapping === undefined) {
      throw new MissingStateMappingError(toVersion);
    }
    const mapping = target.stateMapping;
    const failures: MigrationFailure[] = [];
    const plan: { id: string; stateBefore: string; stateAfter: string }[] = [];
    for (const id of new Set(instanceIds)) {
      const log = this.instances.get(id);
      if (log === undefined) {
        failures.push({ instanceId: id, reason: "unknown instance" });
        continue;
      }
      const current = replay(log, (version) => this.requireGraph(version));
      if (current.graphVersion !== previous.version) {
        failures.push({
          instanceId: id,
          reason: `instance is on graph v${current.graphVersion}, expected v${previous.version}`,
        });
        continue;
      }
      const stateAfter = mapping[current.state];
      if (stateAfter === undefined) {
        failures.push({
          instanceId: id,
          reason: `active state "${current.state}" has no mapping into graph v${toVersion}`,
        });
        continue;
      }
      plan.push({ id, stateBefore: current.state, stateAfter });
    }
    if (failures.length > 0) {
      throw new BatchMigrationError(toVersion, failures);
    }
    const results: MigrationResult[] = [];
    for (const step of plan) {
      const log = this.requireLog(step.id);
      const event: MigratedEvent = {
        kind: "migrated",
        seq: log.length + 1,
        at: this.clock(),
        fromVersion: previous.version,
        toVersion,
        stateBefore: step.stateBefore,
        stateAfter: step.stateAfter,
        stateMapping: { ...mapping },
      };
      log.push(event);
      results.push({
        instanceId: step.id,
        fromVersion: previous.version,
        toVersion,
        stateBefore: step.stateBefore,
        stateAfter: step.stateAfter,
        seq: event.seq,
      });
    }
    return results;
  }

  availableActions(instanceId: string): string[] {
    const snapshot = this.snapshot(instanceId);
    const graph = this.requireGraph(snapshot.graphVersion);
    return graph.transitions.filter((t) => t.from.includes(snapshot.state)).map((t) => t.event);
  }

  audit(instanceId: string): AuditReport {
    return auditLog(this.requireLog(instanceId), (version) => this.requireGraph(version));
  }

  snapshot(instanceId: string): InstanceSnapshot {
    const log = this.requireLog(instanceId);
    const current = replay(log, (version) => this.requireGraph(version));
    return {
      id: instanceId,
      graphVersion: current.graphVersion,
      state: current.state,
      log: [...log],
    };
  }

  private requireGraph(version: number): GraphVersion {
    const graph = this.graphs.get(version);
    if (graph === undefined) {
      throw new UnknownGraphError(version);
    }
    return graph;
  }

  private requireLog(instanceId: string): InstanceEvent[] {
    const log = this.instances.get(instanceId);
    if (log === undefined) {
      throw new UnknownInstanceError(instanceId);
    }
    return log;
  }

  private latestGraph(): GraphVersion {
    let latest: GraphVersion | undefined;
    for (const graph of this.graphs.values()) {
      if (latest === undefined || graph.version > latest.version) {
        latest = graph;
      }
    }
    if (latest === undefined) {
      throw new UnknownGraphError(-1);
    }
    return latest;
  }

  private previousGraph(version: number): GraphVersion | undefined {
    let previous: GraphVersion | undefined;
    for (const graph of this.graphs.values()) {
      if (graph.version < version && (previous === undefined || graph.version > previous.version)) {
        previous = graph;
      }
    }
    return previous;
  }
}
