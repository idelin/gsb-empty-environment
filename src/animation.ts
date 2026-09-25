import type { Ticks } from './time.ts';
import type { KeyframeTrack } from './keyframes.ts';
import type { TimelineEvent } from './events.ts';
import { inCrossingInterval } from './events.ts';
import {
  activeDurationOf,
  directedTimeAt,
  isReversedIteration,
  normalizeTiming,
  reducedDirectedTime,
} from './timing.ts';
import type { NormalizedTiming, Timing } from './timing.ts';

export interface PropertyValues {
  [property: string]: number;
}

/** Common interface of animations and (nested) timelines. */
export interface AnimNode {
  readonly id: string;
  /** Active duration in this node's local frame (Infinity if unbounded). */
  readonly duration: Ticks;
  /** Pure evaluation: writes animated properties for local time `local`. */
  sampleInto(out: PropertyValues, local: Ticks, reduced: boolean): void;
  /** Append events crossed while local time moves `from` -> `to`. */
  collectEvents(from: Ticks, to: Ticks, into: TimelineEvent[], path: string): void;
}

/**
 * Leaf node: a set of keyframe tracks driven by a timing model.
 *
 * Keyframe events are expanded into the animation's local frame across
 * iterations (respecting direction), so event delivery and property sampling
 * observe the same pure time mapping. Coincident events — identical name,
 * property and local time, e.g. produced by adjacent iterations at a period
 * boundary — are delivered once.
 */
export class Animation implements AnimNode {
  readonly id: string;
  readonly timing: NormalizedTiming;
  private tracks: KeyframeTrack[];
  /** Bumped on every hot-swap; invalidates previously collected events. */
  version: number;

  constructor(id: string, tracks: KeyframeTrack[], timing: Timing) {
    this.id = id;
    this.tracks = tracks.slice();
    this.timing = normalizeTiming(timing);
    this.version = 1;
  }

  get duration(): Ticks {
    return activeDurationOf(this.timing);
  }

  get trackList(): readonly KeyframeTrack[] {
    return this.tracks;
  }

  /**
   * Hot-swap a track (same property replaced, new property added). The
   * animation version bumps; playback position is untouched and resumable.
   */
  replaceTrack(track: KeyframeTrack): void {
    this.tracks = this.tracks.filter((t) => t.property !== track.property);
    this.tracks.push(track);
    this.version++;
  }

  sampleInto(out: PropertyValues, local: Ticks, reduced: boolean): void {
    for (const track of this.tracks) {
      const directed = reduced
        ? reducedDirectedTime(this.timing)
        : directedTimeAt(this.timing, local);
      if (directed === null) continue;
      out[track.property] = track.sample(directed);
    }
  }

  collectEvents(from: Ticks, to: Ticks, into: TimelineEvent[], path: string): void {
    if (from === to) return;
    const t = this.timing;
    // Defined: zero-duration animations emit no keyframe events.
    if (t.duration === 0) return;
    const lo = Math.min(from, to);
    const hi = Math.max(from, to);
    const seen = new Set<string>();
    for (const track of this.tracks) {
      for (const key of track.keyframes) {
        if (key.event === undefined) continue;
        // Iterations whose span can intersect [lo, hi].
        const kFirst = Math.max(0, Math.floor((lo - t.delay - t.duration) / t.duration));
        const kLast = Math.min(t.iterations - 1, Math.floor((hi - t.delay) / t.duration));
        for (let k = kFirst; k <= kLast; k++) {
          const reversed = isReversedIteration(t, k);
          const at =
            t.delay + k * t.duration + (reversed ? t.duration - key.at : key.at);
          if (inCrossingInterval(at, from, to)) {
            const dedupeKey = `${at}${track.property}${key.event}`;
            if (seen.has(dedupeKey)) continue;
            seen.add(dedupeKey);
            into.push({
              name: key.event,
              at,
              version: this.version,
              path,
              property: track.property,
            });
          }
        }
      }
    }
  }
}
