import type { Packet, QueueId } from './packet.ts';
import { classifyDscp } from './classifier.ts';

type AfQueueId = 'AF1' | 'AF2' | 'AF3' | 'AF4';

export interface SchedulerConfig {
  /** WRR weights per AF class. Defaults: AF4=4, AF3=3, AF2=2, AF1=1. */
  afWeights?: Partial<Record<AfQueueId, number>>;
}

const AF_QUEUES: AfQueueId[] = ['AF1', 'AF2', 'AF3', 'AF4'];
const DEFAULT_AF_WEIGHTS: Record<AfQueueId, number> = {
  AF1: 1, AF2: 2, AF3: 3, AF4: 4,
};

/**
 * DiffServ egress scheduler:
 *  1. EF queue is drained with strict priority (voice SLA).
 *  2. AF classes share remaining capacity via smooth weighted round robin.
 *  3. BE is served only when EF and all AF queues are empty.
 */
export class DiffServScheduler {
  private readonly queues: Record<QueueId, Packet[]> = {
    EF: [], AF1: [], AF2: [], AF3: [], AF4: [], BE: [],
  };
  private readonly weights: Record<AfQueueId, number>;
  private readonly smoothCurrent: Record<AfQueueId, number> = {
    AF1: 0, AF2: 0, AF3: 0, AF4: 0,
  };

  constructor(config: SchedulerConfig = {}) {
    this.weights = { ...DEFAULT_AF_WEIGHTS, ...config.afWeights };
    for (const q of AF_QUEUES) {
      if (!(this.weights[q] > 0)) {
        throw new RangeError(`AF weight for ${q} must be positive`);
      }
    }
  }

  /** Classify a packet by DSCP and enqueue it. Returns the queue used. */
  enqueue(packet: Packet): QueueId {
    const queueId = classifyDscp(packet.dscp);
    this.queues[queueId].push(packet);
    return queueId;
  }

  /** Pick the next packet to transmit, or undefined when all queues are empty. */
  dequeue(): Packet | undefined {
    if (this.queues.EF.length > 0) return this.queues.EF.shift();

    const af = this.pickAfQueue();
    if (af !== undefined) return this.queues[af].shift();

    if (this.queues.BE.length > 0) return this.queues.BE.shift();
    return undefined;
  }

  /** Smooth WRR (nginx-style) over the non-empty AF queues. */
  private pickAfQueue(): AfQueueId | undefined {
    let best: AfQueueId | undefined;
    let totalWeight = 0;
    for (const q of AF_QUEUES) {
      if (this.queues[q].length === 0) continue;
      this.smoothCurrent[q] += this.weights[q];
      totalWeight += this.weights[q];
      if (best === undefined || this.smoothCurrent[q] > this.smoothCurrent[best]) {
        best = q;
      }
    }
    if (best === undefined) return undefined;
    this.smoothCurrent[best] -= totalWeight;
    return best;
  }

  queueLength(queueId: QueueId): number {
    return this.queues[queueId].length;
  }

  get backlog(): number {
    let total = 0;
    for (const id of Object.keys(this.queues) as QueueId[]) {
      total += this.queues[id].length;
    }
    return total;
  }
}
