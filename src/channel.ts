import type { StoragePartition } from "./partition.ts";
import type { BroadcastEnvelope, BroadcastRouter } from "./router.ts";
import { defaultRouter } from "./router.ts";

export interface BroadcastMessage<T = unknown> {
  data: T;
  partitionKey: string;
  channelName: string;
  senderId: string | null;
  enqueueOrder: number;
}

export type ChannelListener<T = unknown> = (message: BroadcastMessage<T>) => void;

export interface ChannelOptions {
  router?: BroadcastRouter;
}

let channelCounter = 0;

export class PartitionedBroadcastChannel<T = unknown> {
  readonly id: string;
  readonly channelName: string;
  readonly partition: StoragePartition;
  private readonly router: BroadcastRouter;
  private listeners = new Set<ChannelListener<T>>();
  private closed = false;

  constructor(channelName: string, partition: StoragePartition, options: ChannelOptions = {}) {
    this.id = `bc-${channelCounter++}`;
    this.channelName = channelName;
    this.partition = partition;
    this.router = options.router ?? defaultRouter;
    this.router.attach(this);
  }

  get partitionKey(): string {
    return this.partition.key;
  }

  get isClosed(): boolean {
    return this.closed;
  }

  addListener(listener: ChannelListener<T>): void {
    if (this.closed) {
      throw new TypeError("channel is closed");
    }
    this.listeners.add(listener);
  }

  removeListener(listener: ChannelListener<T>): void {
    this.listeners.delete(listener);
  }

  postMessage(data: T): void {
    if (this.closed) {
      throw new TypeError("channel is closed");
    }
    this.router.post({
      channelName: this.channelName,
      partitionKey: this.partition.key,
      data,
      senderId: this.id,
    });
  }

  close(): void {
    if (this.closed) {
      return;
    }
    this.closed = true;
    this.listeners.clear();
    this.router.detach(this);
  }

  deliver(envelope: BroadcastEnvelope): void {
    if (this.closed) {
      return;
    }
    const message: BroadcastMessage<T> = {
      data: envelope.data as T,
      partitionKey: envelope.partitionKey,
      channelName: envelope.channelName,
      senderId: envelope.senderId,
      enqueueOrder: envelope.enqueueOrder,
    };
    const snapshot = Array.from(this.listeners);
    for (const listener of snapshot) {
      listener(message);
    }
  }
}
