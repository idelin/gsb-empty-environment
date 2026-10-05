/**
 * Cross-checks the incremental engine against the independent reference
 * resolver: for every module in a snapshot, every exported name and every
 * explicit import is resolved by both implementations and must agree.
 */
import type { Resolution } from './types.ts';
import type { Snapshot } from './project.ts';
import { resolveReference } from './reference.ts';
import { canonicalChain } from './resolver.ts';

export interface Mismatch {
  readonly module: string;
  readonly name: string;
  readonly site: 'export' | 'import';
  readonly main: Resolution;
  readonly reference: Resolution;
}

export function canonicalResolution(r: Resolution): string {
  switch (r.status) {
    case 'unresolved':
      return 'unresolved';
    case 'circular':
      // Cycle witnesses may legitimately differ between implementations.
      return 'circular';
    case 'resolved':
      return `resolved ${r.origin.module}#${r.origin.name} via ${canonicalChain(r.chain)}`;
    case 'conflict':
      return `conflict ${r.definitions
        .map((d) => `${d.origin.module}#${d.origin.name} via ${canonicalChain(d.chain)}`)
        .join(' | ')}`;
  }
}

export function validateSnapshot(snapshot: Snapshot): Mismatch[] {
  const mismatches: Mismatch[] = [];
  const check = (site: 'export' | 'import', module: string, name: string, target: string, targetName: string) => {
    const main = snapshot.resolveExport(target, targetName);
    const reference = resolveReference(snapshot.modules, target, targetName);
    if (canonicalResolution(main) !== canonicalResolution(reference)) {
      mismatches.push({ module, name, site, main, reference });
    }
  };
  for (const id of snapshot.moduleIds()) {
    const spec = snapshot.moduleSpec(id)!;
    const exportNames = new Set<string>();
    for (const d of spec.definitions ?? []) exportNames.add(d);
    for (const re of spec.reexports ?? []) exportNames.add(re.as ?? re.name);
    for (const name of exportNames) check('export', id, name, id, name);
    for (const imp of spec.imports ?? []) check('import', id, imp.name, imp.from, imp.name);
  }
  return mismatches;
}
