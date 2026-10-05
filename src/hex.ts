// Finite axial-coordinate hex map helpers.
// Axial coordinates (q, r); the third cube coordinate is s = -q - r.

export interface Axial {
  q: number;
  r: number;
}

export function keyOf(a: Axial): string {
  return `${a.q},${a.r}`;
}

export function parseKey(k: string): Axial {
  const parts = k.split(",");
  if (parts.length !== 2) throw new Error(`bad cell key: ${k}`);
  const q = Number(parts[0]);
  const r = Number(parts[1]);
  if (!Number.isInteger(q) || !Number.isInteger(r)) throw new Error(`bad cell key: ${k}`);
  return { q, r };
}

// All cells of a hexagon-shaped finite map with the given radius.
// A cell (q, r) is in the map iff max(|q|, |r|, |q + r|) <= radius.
export function hexagonKeys(radius: number): string[] {
  if (!Number.isInteger(radius) || radius < 0) throw new Error(`bad radius: ${radius}`);
  const out: string[] = [];
  for (let q = -radius; q <= radius; q++) {
    const lo = Math.max(-radius, -q - radius);
    const hi = Math.min(radius, -q + radius);
    for (let r = lo; r <= hi; r++) out.push(keyOf({ q, r }));
  }
  return out.sort();
}
