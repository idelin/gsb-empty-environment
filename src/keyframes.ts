import type { Ticks } from './time.ts';
import { assertTicks } from './time.ts';
import { linear } from './easing.ts';
import type { Easing } from './easing.ts';

export interface Keyframe {
  /** Offset time within the track, in ticks. Must be a non-negative integer. */
  at: Ticks;
  value: number;
  /** Easing applied from this keyframe to the next one. Defaults to linear. */
  easing?: Easing;
  /** Optional event name emitted when playback crosses this keyframe. */
  event?: string;
}

/**
 * A single-property keyframe track. Sampling is a pure function of track time,
 * so random access and sequential evaluation always agree.
 *
 * Tracks are hot-swappable: `replaceKeyframes` bumps `version`, which
 * invalidates every event previously derived from this track.
 */
export class KeyframeTrack {
  readonly property: string;
  private keys: Keyframe[];
  version: number;

  constructor(property: string, keys: Keyframe[]) {
    this.property = property;
    this.version = 1;
    this.keys = [];
    this.replaceKeyframes(keys);
  }

  get keyframes(): readonly Keyframe[] {
    return this.keys;
  }

  /** Hot-swap the keyframes. Bumps `version` so old events become invalid. */
  replaceKeyframes(keys: Keyframe[]): void {
    if (keys.length === 0) {
      throw new Error(`track "${this.property}" needs at least one keyframe`);
    }
    const sorted = keys.slice().sort((a, b) => a.at - b.at);
    for (const k of sorted) {
      assertTicks(k.at, `keyframe time of "${this.property}"`);
      if (k.at < 0) {
        throw new Error(`keyframe time must be >= 0, got ${k.at}`);
      }
    }
    this.keys = sorted;
    this.version++;
  }

  get firstTime(): Ticks {
    return this.keys[0].at;
  }

  get lastTime(): Ticks {
    return this.keys[this.keys.length - 1].at;
  }

  /** Pure sample at track time `t`. Clamps outside the keyframe range. */
  sample(t: Ticks): number {
    const keys = this.keys;
    const first = keys[0];
    const last = keys[keys.length - 1];
    if (t <= first.at) return first.value;
    if (t >= last.at) return last.value;
    let lo = 0;
    let hi = keys.length - 1;
    while (lo + 1 < hi) {
      const mid = (lo + hi) >> 1;
      if (keys[mid].at <= t) lo = mid;
      else hi = mid;
    }
    const k0 = keys[lo];
    const k1 = keys[lo + 1];
    const span = k1.at - k0.at;
    if (span === 0) return k1.value;
    const p = (t - k0.at) / span;
    const eased = (k0.easing ?? linear)(p);
    return k0.value + (k1.value - k0.value) * eased;
  }
}
