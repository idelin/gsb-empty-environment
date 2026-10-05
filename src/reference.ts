import type { Hop, Resolution, Snapshot } from './types.ts';

/**
 * Independent cache-free reference implementation used to cross-check the
 * cached resolver. Deliberately shares no code with the traversal core.
 */
export function referenceResolve(
  snap: Snapshot,
  start: string,
  maxHops: number,
): Resolution {
  const path: Hop[] = [];
  const visit = (current: string): Resolution => {
    const target = snap.aliases.get(current);
    if (target === undefined) {
      if (path.length === 0) return { status: 'missing-alias', name: current };
      return { status: 'dangling', path: [...path], missing: { kind: 'alias', name: current } };
    }
    const prior = path.findIndex((hop) => hop.alias === current);
    if (prior !== -1) {
      return {
        status: 'cycle',
        path: [...path],
        cycle: [...path.slice(prior).map((hop) => hop.alias), current],
      };
    }
    path.push({ alias: current, target });
    if (target.kind === 'object') {
      const versions = snap.objects.get(target.name);
      if (versions === undefined || !versions.has(target.version)) {
        return { status: 'dangling', path: [...path], missing: target };
      }
      return {
        status: 'ok',
        path: [...path],
        object: { name: target.name, version: target.version },
      };
    }
    if (path.length >= maxHops) {
      return { status: 'max-hops-exceeded', path: [...path] };
    }
    return visit(target.name);
  };
  return visit(start);
}
