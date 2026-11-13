import { createDeterministicRng, type RandomNumberGenerator } from './rng.ts';

export interface BrowsingTopic {
  topic: number;
  version: string;
}

export interface Observation {
  topLevelSite: string;
  callerOrigin: string;
  topic: number;
}

export interface BrowsingTopicsCallerContext {
  /** Origin of the script invoking document.browsingTopics(). */
  callerOrigin: string;
  /** Registrable site of the page whose document the call runs on. */
  topLevelSite: string;
  /**
   * Whether the browsing-topics Permissions Policy allows this document.
   * A cross-origin iframe without an allow="browsing-topics" header is false.
   */
  topicsPolicyAllowed: boolean;
}

export interface TopicsHostOptions {
  /** Chrome-style defaults: 5% per-epoch filtering, 5% random topic. */
  filterProbability?: number;
  randomTopicProbability?: number;
  taxonomyVersion?: string;
  epochDurationMs?: number;
  /** Clock injection for deterministic tests. */
  now?: () => number;
  /** RNG for generating independent epoch seeds. */
  randomSeed?: () => string;
}

interface EpochState {
  index: number;
  seed: string;
  startedAt: number;
  /** topic -> set of caller origins that observed the topic this epoch. */
  observations: Map<number, Set<string>>;
}

const DEFAULT_FILTER_PROBABILITY = 0.05;
const DEFAULT_RANDOM_TOPIC_PROBABILITY = 0.05;
const DEFAULT_TAXONOMY_VERSION = 'chrome.1';
const WEEK_MS = 7 * 24 * 60 * 60 * 1000;
const RETURNED_EPOCH_LIMIT = 3;

function defaultSeed(): string {
  const bytes = new Uint8Array(32);
  crypto.getRandomValues(bytes);
  return Array.from(bytes, (byte) => byte.toString(16).padStart(2, '0')).join('');
}

/**
 * In-browser host side of the Topics API.
 *
 * Storage model: every weekly epoch keeps a map of topic -> the set of
 * callers that observed that topic. A caller only ever receives topics that
 * it itself observed in that epoch, so a freshly embedded third-party script
 * cannot read topics it has never seen.
 */
export class TopicsHost {
  private readonly filterProbability: number;
  private readonly randomTopicProbability: number;
  private readonly taxonomyVersion: string;
  private readonly epochDurationMs: number;
  private readonly now: () => number;
  private readonly randomSeed: () => string;

  private readonly epochs: EpochState[] = [];
  private currentEpochIndex = 0;

  constructor(options: TopicsHostOptions = {}) {
    this.filterProbability = options.filterProbability ?? DEFAULT_FILTER_PROBABILITY;
    this.randomTopicProbability = options.randomTopicProbability ?? DEFAULT_RANDOM_TOPIC_PROBABILITY;
    this.taxonomyVersion = options.taxonomyVersion ?? DEFAULT_TAXONOMY_VERSION;
    this.epochDurationMs = options.epochDurationMs ?? WEEK_MS;
    this.now = options.now ?? Date.now;
    this.randomSeed = options.randomSeed ?? defaultSeed;
    this.epochs.push(this.createEpoch(0, this.now()));
  }

  private createEpoch(index: number, startedAt: number, seed?: string): EpochState {
    return {
      index,
      seed: seed ?? this.randomSeed(),
      startedAt,
      observations: new Map<number, Set<string>>(),
    };
  }

  /** Close the current epoch, generate a fresh independent seed, start the next. */
  advanceEpoch(seed?: string): number {
    const nextIndex = this.currentEpochIndex + 1;
    this.epochs.push(this.createEpoch(nextIndex, this.now(), seed));
    this.currentEpochIndex = nextIndex;
    this.pruneClosedEpochs();
    return nextIndex;
  }

  /**
   * Roll the epoch forward based on wall-clock time (one epoch per week).
   * Returns the number of epochs advanced.
   */
  rollEpochsByTime(): number {
    const current = this.epochs[this.epochs.length - 1];
    const elapsed = this.now() - current.startedAt;
    const count = Math.floor(elapsed / this.epochDurationMs);
    for (let step = 0; step < count; step += 1) {
      this.advanceEpoch();
    }
    return count;
  }

  /** Keep only the three most recently closed epochs plus the current one. */
  private pruneClosedEpochs(): void {
    while (this.epochs.length > RETURNED_EPOCH_LIMIT + 1) {
      this.epochs.shift();
    }
  }

  /**
   * Record that a caller observed a topic on a top-level site during the
   * current (in-progress) epoch.
   */
  recordObservation(observation: Observation): void {
    const currentEpoch = this.epochs[this.epochs.length - 1];
    const callers = currentEpoch.observations.get(observation.topic) ?? new Set<string>();
    callers.add(observation.callerOrigin);
    currentEpoch.observations.set(observation.topic, callers);
  }

  private closedEpochs(): EpochState[] {
    return this.epochs.slice(0, -1).slice(-RETURNED_EPOCH_LIMIT);
  }

  /**
   * Implements document.browsingTopics().
   *
   * For each of the last three weekly epochs the caller's observed topics in
   * that epoch are collected. The per-(top-level site, caller, epoch) random
   * decisions (filtering vs. keep, and which observed topic is returned) are
   * drawn from an RNG seeded by that epoch's independent host seed, so
   * repeated calls with the same tuple are stable while different epochs stay
   * uncorrelatable.
   */
  async fetchTopics(context: BrowsingTopicsCallerContext): Promise<BrowsingTopic[]> {
    if (!context.topicsPolicyAllowed) {
      return [];
    }

    const results: BrowsingTopic[] = [];
    for (const epoch of this.closedEpochs()) {
      const observed = this.topicsObservedByCaller(epoch, context.callerOrigin);
      if (observed.length === 0) {
        continue;
      }

      const rng = await createDeterministicRng(
        epoch.seed,
        context.topLevelSite,
        context.callerOrigin,
      );
      const topic = this.pickTopic(observed, rng);
      if (topic !== null) {
        results.push({ topic, version: this.taxonomyVersion });
      }
    }
    return results;
  }

  private topicsObservedByCaller(epoch: EpochState, callerOrigin: string): number[] {
    const topics: number[] = [];
    for (const [topic, callers] of epoch.observations) {
      if (callers.has(callerOrigin)) {
        topics.push(topic);
      }
    }
    return topics.sort((left, right) => left - right);
  }

  /**
   * Returns null when the epoch is filtered out for this caller draw.
   * Filtering and random-topic draws both consume the deterministic stream,
   * and every candidate comes from the caller's own observed set, so no
   * never-observed topic can leak.
   */
  private pickTopic(observedTopics: number[], rng: RandomNumberGenerator): number | null {
    if (rng() < this.filterProbability) {
      return null;
    }
    if (rng() < this.randomTopicProbability) {
      return observedTopics[Math.floor(rng() * observedTopics.length)];
    }
    return observedTopics[0];
  }
}
