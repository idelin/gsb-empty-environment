import { resolveEasing } from './easing.ts';
import type { Easing, EasingName } from './easing.ts';

export interface Keyframe {
  offset: number;
  easing?: EasingName | Easing;
  props: Record<string, number>;
}

const clamp01 = (v: number) => (v < 0 ? 0 : v > 1 ? 1 : v);

export class KeyframeTrack {
  readonly keyframes: readonly Keyframe[];
  readonly properties: readonly string[];

  constructor(keyframes: Keyframe[]) {
    if (keyframes.length === 0) throw new Error('at least one keyframe is required');
    for (const kf of keyframes) {
      if (!(kf.offset >= 0 && kf.offset <= 1)) {
        throw new Error(`keyframe offset out of range: ${kf.offset}`);
      }
    }
    this.keyframes = [...keyframes].sort((a, b) => a.offset - b.offset);
    const props = new Set<string>();
    for (const kf of this.keyframes) {
      for (const name of Object.keys(kf.props)) props.add(name);
    }
    this.properties = [...props].sort();
  }

  // Samples the forward track at progress p in [0, 1]. Reversed playback is
  // obtained by the caller as sample(1 - p), i.e. by mirroring time.
  sample(progress: number): Record<string, number> {
    const kfs = this.keyframes;
    const p = clamp01(progress);
    const first = kfs[0];
    const last = kfs[kfs.length - 1];
    if (p <= first.offset) return { ...first.props };
    if (p >= last.offset) return { ...last.props };

    let lo = first;
    let hi = last;
    for (let i = 0; i < kfs.length - 1; i++) {
      if (p >= kfs[i].offset && p <= kfs[i + 1].offset) {
        lo = kfs[i];
        hi = kfs[i + 1];
        break;
      }
    }
    const span = hi.offset - lo.offset;
    const local = span === 0 ? 1 : (p - lo.offset) / span;
    const eased = resolveEasing(lo.easing)(local);

    const out: Record<string, number> = {};
    for (const name of this.properties) {
      const a = name in lo.props ? lo.props[name] : hi.props[name];
      const b = name in hi.props ? hi.props[name] : lo.props[name];
      out[name] = a + (b - a) * eased;
    }
    return out;
  }
}
