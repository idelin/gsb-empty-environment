export interface Dot {
  node: string;
  counter: number;
}

export type VersionVector = Record<string, number>;

export function covers(vv: VersionVector, dot: Dot): boolean {
  return (vv[dot.node] ?? 0) >= dot.counter;
}

export function merge(a: VersionVector, b: VersionVector): VersionVector {
  const out: VersionVector = { ...a };
  for (const [node, counter] of Object.entries(b)) {
    if ((out[node] ?? 0) < counter) out[node] = counter;
  }
  return out;
}

export function mergeDot(vv: VersionVector, dot: Dot): VersionVector {
  if ((vv[dot.node] ?? 0) >= dot.counter) return vv;
  return { ...vv, [dot.node]: dot.counter };
}

export function dominates(a: VersionVector, b: VersionVector): boolean {
  for (const [node, counter] of Object.entries(b)) {
    if ((a[node] ?? 0) < counter) return false;
  }
  return true;
}

export type Causality = 'equal' | 'a-descends' | 'b-descends' | 'concurrent';

export function compare(a: VersionVector, b: VersionVector): Causality {
  const ab = dominates(a, b);
  const ba = dominates(b, a);
  if (ab && ba) return 'equal';
  if (ab) return 'a-descends';
  if (ba) return 'b-descends';
  return 'concurrent';
}

export function dotKey(dot: Dot): string {
  return `${dot.node}#${dot.counter}`;
}

export function compareDots(a: Dot, b: Dot): number {
  if (a.node !== b.node) return a.node < b.node ? -1 : 1;
  return a.counter - b.counter;
}
