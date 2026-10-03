import { DscpClassifier } from './classifier.ts';
import type { Packet, QueueId } from './packet.ts';

/** WRR weights for the four AF classes. */
export interface AfWeights {
  AF4: number;
  AF3: number;
  AF2: number;
  AF1: number;
}

export const DEFAULT_AF_WEIGHTS: AfWeights = { AF4: 4, AF3: 3, AF2: 2, AF1: 1 };

const AF_ORDER = ['AF4', 'AF3', 'AF2', 'AF1'] as const;
type AfQueueId = (typeof AF_ORDER)[number];

export interface DequeuedPacket {
  packet: Packet;
  queue: QueueId;
}

/**
 * DiffServ egress scheduler.
 *
 * Discipline (per dequeue):
 *   1. EF queue is drained with strict (non-preemptive at packet level) priority.
 *   2. AF classes are served with smooth weighted round robin (WRR).
 *   3. BE is served only when EF and all AF classes are empty.
 */
export class DiffServScheduler {
  private readonly classifier: DscpClassifier;
  private readonly weights: AfWeights;
  private readonly queues: Record<QueueId, Packet[]>;
  private readonly smoothState: Record<AfQueueId, number>;

  constructor(options?: { classifier?: DscpClassifier; afWeights?: Partial<AfWeights> }) {
    this.classifier = options?.classifier ?? new DscpClassifier();
    this.weights = { ...DEFAULT_AF_WEIGHTS, ...options?.afWeights };
    for (const id of AF_ORDER) {
      if (!Number.isFinite(this.weights[id]) || this.weights[id] <= 0) {
        throw new RangeError(`AF weight for ${id} must be a positive number`);
      }
    }
    this.queues = { EF: [], AF4: [], AF3: [], AF2: [], AF1: [], BE: [] };
    this.smoothState = { AF4: 0, AF3: 0, AF2: 0, AF1: 0 };
  }

  /** Classify an incoming packet and append it to its queue. */
  enqueue(packet: Packet): QueueId {
    const queue = this.classifier.classify(packet);
    this.queues[queue].push(packet);
    return queue;
  }

  /** Pick the next packet to transmit according to the scheduling discipline. */
  dequeue(): DequeuedPacket | undefined {
    if (this.queues.EF.length > 0) {
      return { packet: this.queues.EF.shift()!, queue: 'EF' };
    }
    const af = this.dequeueAf();
    if (af) return af;
    if (this.queues.BE.length > 0) {
      return { packet: this.queues.BE.shift()!, queue: 'BE' };
    }
    return undefined;
  }

  /**
   * Smooth WRR (nginx-style): each non-empty class accumulates its weight,
   * the class with the highest accumulator wins and pays the total weight.
   * Produces an evenly interleaved schedule with exact long-term ratios.
   */
  private dequeueAf(): DequeuedPacket | undefined {
    let best: AfQueueId | undefined;
    let totalWeight = 0;
    for (const id of AF_ORDER) {
      if (this.queues[id].length === 0) continue;
      totalWeight += this.weights[id];
      this.smoothState[id] += this.weights[id];
      if (best === undefined || this.smoothState[id] > this.smoothState[best]) {
        best = id;
      }
    }
    if (best === undefined) return undefined;
    this.smoothState[best] -= totalWeight;
    return { packet: this.queues[best].shift()!, queue: best };
  }

  /** Total number of queued packets across all classes. */
  get size(): number {
    let total = 0;
    for (const id of Object.keys(this.queues) as QueueId[]) total += this.queues[id].length;
    return total;
  }

  backlog(queue: QueueId): number {
    return this.queues[queue].length;
  }
}
