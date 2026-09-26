/**
 * Boundary policies applied ONLY at image edges. Tile edges are never
 * treated as image edges: cross-tile samples are read from neighbour tiles.
 *
 * All mappings are closed-form arithmetic (no iterative reflection), so
 * degenerate sizes such as width-1 images terminate immediately.
 */
export type BoundaryPolicy = 'clamp' | 'zero' | 'wrap' | 'mirror' | 'reflect101';

export const BOUNDARY_POLICIES: readonly BoundaryPolicy[] = [
  'clamp',
  'zero',
  'wrap',
  'mirror',
  'reflect101',
];

/**
 * Map a sample coordinate to an in-image coordinate.
 * Returns -1 when the sample contributes zero ('zero' policy, out of range).
 */
export function mapIndex(i: number, n: number, policy: BoundaryPolicy): number {
  if (n <= 0) throw new Error('mapIndex: empty dimension');
  if (i >= 0 && i < n) return i;
  switch (policy) {
    case 'clamp':
      return i < 0 ? 0 : n - 1;
    case 'zero':
      return -1;
    case 'wrap': {
      const m = i % n;
      return m < 0 ? m + n : m;
    }
    case 'mirror': {
      // half-sample symmetric: -1 -> 0, -2 -> 1, n -> n-1
      const period = 2 * n;
      let r = i % period;
      if (r < 0) r += period;
      return r < n ? r : period - 1 - r;
    }
    case 'reflect101': {
      // whole-sample symmetric: -1 -> 1, -2 -> 2, n -> n-2
      if (n === 1) return 0;
      const period = 2 * n - 2;
      let r = i % period;
      if (r < 0) r += period;
      return r < n ? r : period - r;
    }
  }
}
