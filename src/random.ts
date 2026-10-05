export type Rng = () => number;

export function mulberry32(seed: number): Rng {
  let state = seed >>> 0;
  return () => {
    state = (state + 0x6d2b79f5) >>> 0;
    let t = state;
    t = Math.imul(t ^ (t >>> 15), t | 1);
    t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

export type DelaySampler = (now: number) => number;

export function constantDelay(ms: number): DelaySampler {
  return () => ms;
}

export function uniformDelay(rng: Rng, minMs: number, maxMs: number): DelaySampler {
  return () => minMs + rng() * (maxMs - minMs);
}

export function lognormalDelay(rng: Rng, mu: number, sigma: number): DelaySampler {
  return () => {
    const u1 = Math.max(rng(), 1e-12);
    const u2 = rng();
    const z = Math.sqrt(-2 * Math.log(u1)) * Math.cos(2 * Math.PI * u2);
    return Math.exp(mu + sigma * z);
  };
}

export interface CongestionOptions {
  episodeProbability: number;
  episodeMinMs: number;
  episodeMaxMs: number;
  congestedMinMs: number;
  congestedMaxMs: number;
  baseMinMs: number;
  baseMaxMs: number;
}

export function congestedDelay(rng: Rng, opts: CongestionOptions): DelaySampler {
  let congestedUntil = -Infinity;
  return (now: number) => {
    if (now >= congestedUntil && rng() < opts.episodeProbability) {
      congestedUntil = now + opts.episodeMinMs + rng() * (opts.episodeMaxMs - opts.episodeMinMs);
    }
    if (now < congestedUntil) {
      return opts.congestedMinMs + rng() * (opts.congestedMaxMs - opts.congestedMinMs);
    }
    return opts.baseMinMs + rng() * (opts.baseMaxMs - opts.baseMinMs);
  };
}
