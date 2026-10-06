import type {
  EventEnvelope,
  EventPayload,
  GraphVersion,
  InstanceId,
} from "./types";
import { CorruptLogError, UnknownInstanceError } from "./errors";

/**
 * Append-only, per-instance event log. Appends are synchronous, so a batch
 * of appends issued inside one call is atomic with respect to readers.
 */
export class InMemoryEventStore {
  private readonly logs = new Map<InstanceId, EventEnvelope[]>();

  has(instanceId: InstanceId): boolean {
    return this.logs.has(instanceId);
  }

  append<P extends EventPayload>(
    instanceId: InstanceId,
    version: GraphVersion,
    payload: P,
    recordedAt: string = new Date().toISOString(),
  ): EventEnvelope<P> {
    const log = this.logs.get(instanceId) ?? [];
    const envelope: EventEnvelope<P> = {
      seq: log.length + 1,
      instanceId,
      version,
      payload,
      recordedAt,
    };
    log.push(envelope);
    this.logs.set(instanceId, log);
    return envelope;
  }

  load(instanceId: InstanceId): readonly EventEnvelope[] {
    const log = this.logs.get(instanceId);
    if (!log) {
      throw new UnknownInstanceError(instanceId);
    }
    return log;
  }

  /** Rehydrate a previously persisted log (e.g. exported from an old deploy). */
  restore(instanceId: InstanceId, log: readonly EventEnvelope[]): void {
    log.forEach((envelope, index) => {
      if (envelope.instanceId !== instanceId) {
        throw new CorruptLogError(
          `Event #${index + 1} belongs to "${envelope.instanceId}", not "${instanceId}".`,
        );
      }
      if (envelope.seq !== index + 1) {
        throw new CorruptLogError(
          `Event sequence gap in log for "${instanceId}" at position ${index + 1}.`,
        );
      }
    });
    this.logs.set(instanceId, [...log]);
  }

  export(instanceId: InstanceId): readonly EventEnvelope[] {
    return [...this.load(instanceId)];
  }
}
