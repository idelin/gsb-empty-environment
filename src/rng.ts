export type Rng = () => number;

export function mulberry32(seed: number): Rng {
  let a = seed >>> 0;
  return () => {
    a = (a + 0x6d2b79f5) >>> 0;
    let t = a;
    t = Math.imul(t ^ (t >>> 15), t | 1);
    t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

export function uniform(rng: Rng, min: number, max: number): number {
  return min + rng() * (max - min);
}

export function normal(rng: Rng): number {
  let u = 0;
  while (u === 0) u = rng();
  return Math.sqrt(-2 * Math.log(u)) * Math.cos(2 * Math.PI * rng());
}

export function lognormal(rng: Rng, median: number, sigma: number): number {
  return median * Math.exp(sigma * normal(rng));
}

export function pareto(rng: Rng, xm: number, alpha: number): number {
  return xm / Math.pow(1 - rng(), 1 / alpha);
}
