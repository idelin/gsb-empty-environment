/** Axial coordinates (q, r) on a finite hex-shaped map of a given radius. */

export type CellKey = string; // canonical "q,r"

export function keyOf(q: number, r: number): CellKey {
  return `${q},${r}`;
}

export function parseKey(key: CellKey): { q: number; r: number } {
  const i = key.indexOf(",");
  return { q: Number(key.slice(0, i)), r: Number(key.slice(i + 1)) };
}

export function inMap(q: number, r: number, radius: number): boolean {
  const s = -q - r;
  return Math.abs(q) <= radius && Math.abs(r) <= radius && Math.abs(s) <= radius;
}

export function inMapKey(key: CellKey, radius: number): boolean {
  const { q, r } = parseKey(key);
  return inMap(q, r, radius);
}

export function allCells(radius: number): CellKey[] {
  const out: CellKey[] = [];
  for (let q = -radius; q <= radius; q++) {
    for (let r = -radius; r <= radius; r++) {
      if (inMap(q, r, radius)) out.push(keyOf(q, r));
    }
  }
  return out.sort();
}

/** FNV-1a 32-bit: stable default cell -> partition assignment. */
export function hashKey(key: CellKey): number {
  let h = 0x811c9dc5;
  for (let i = 0; i < key.length; i++) {
    h ^= key.charCodeAt(i);
    h = Math.imul(h, 0x01000193);
  }
  return h >>> 0;
}
