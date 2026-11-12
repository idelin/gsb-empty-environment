import type { PartitionKey } from "./partition.ts";

export interface BroadcastEnvelope {
  readonly channelName: string;
  readonly partitionKey: PartitionKey;
  readonly data: unknown;
  readonly senderId: string | null;
  readonly enqueueOrder: number;
}

export interface RoutableChannel {
  readonly id: string;
  readonly channelName: string;
  readonly partitionKey: PartitionKey;
  readonly isClosed: boolean;
  deliver(envelope: BroadcastEnvelope): void;
}

export interface DeliveryRecord {
  envelope: BroadcastEnvelope;
  recipientIds: string[];
  error: AggregateError | null;
}

export interface BroadcastRouter {
  attach(channel: RoutableChannel): void;
  detach(channel: RoutableChannel): void;
  post(envelope: Omit<BroadcastEnvelope, "enqueueOrder">): BroadcastEnvelope;
  flush(): Promise<void>;
  readonly pendingCount: number;
  readonly delivered: readonly DeliveryRecord[];
}

export function createBroadcastRouter(): BroadcastRouter {
  const channels = new Set<RoutableChannel>();
  const queue: BroadcastEnvelope[] = [];
  const records: DeliveryRecord[] = [];
  let sequence = 0;
  let drainScheduled = false;

  function scheduleDrain(): void {
    if (drainScheduled) {
      return;
    }
    drainScheduled = true;
    queueMicrotask(drain);
  }

  function drain(): void {
    drainScheduled = false;
    while (queue.length > 0) {
      const envelope = queue.shift() as BroadcastEnvelope;
      const recipients = Array.from(channels).filter(
        (channel) =>
          channel.partitionKey === envelope.partitionKey &&
          channel.channelName === envelope.channelName &&
          channel.id !== envelope.senderId,
      );
      const recipientIds = recipients.map((channel) => channel.id);
      const failures: Error[] = [];
      for (const channel of recipients) {
        if (channel.isClosed) {
          continue;
        }
        try {
          channel.deliver(envelope);
        } catch (error) {
          failures.push(error instanceof Error ? error : new Error(String(error)));
        }
      }
      records.push({
        envelope,
        recipientIds,
        error: failures.length > 0 ? new AggregateError(failures, "delivery failed") : null,
      });
    }
  }

  return {
    attach(channel: RoutableChannel): void {
      channels.add(channel);
    },

    detach(channel: RoutableChannel): void {
      channels.delete(channel);
    },

    post(envelope: Omit<BroadcastEnvelope, "enqueueOrder">): BroadcastEnvelope {
      const queued: BroadcastEnvelope = { ...envelope, enqueueOrder: sequence++ };
      queue.push(queued);
      scheduleDrain();
      return queued;
    },

    async flush(): Promise<void> {
      const seen = records.length;
      while (queue.length > 0 || drainScheduled) {
        await Promise.resolve();
      }
      const failed = records.slice(seen).find((record) => record.error !== null)?.error;
      if (failed) {
        throw failed;
      }
    },

    get pendingCount(): number {
      return queue.length;
    },

    get delivered(): readonly DeliveryRecord[] {
      return records;
    },
  };
}

export const defaultRouter: BroadcastRouter = createBroadcastRouter();
