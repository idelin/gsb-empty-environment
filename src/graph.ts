import type { TraceEvent } from './types';

export function indexById(events: TraceEvent[]): Map<string, TraceEvent> {
  const m: Map<string, TraceEvent> = new Map();
  for (const e of events) m.set(e.id, e);
  return m;
}

export function causalPast(ids: string[], index: Map<string, TraceEvent>): Set<string> {
  const seen: Set<string> = new Set();
  const stack: string[] = ids.slice();
  while (stack.length > 0) {
    const id = stack.pop();
    if (id === undefined) break;
    if (seen.has(id)) continue;
    const e = index.get(id);
    if (!e) continue;
    seen.add(id);
    for (const c of e.causes) stack.push(c);
  }
  return seen;
}

export function missingInPast(ids: string[], index: Map<string, TraceEvent>): Set<string> {
  const missing: Set<string> = new Set();
  const seen: Set<string> = new Set();
  const stack: string[] = ids.slice();
  while (stack.length > 0) {
    const id = stack.pop();
    if (id === undefined) break;
    if (seen.has(id)) continue;
    seen.add(id);
    const e = index.get(id);
    if (!e) {
      missing.add(id);
      continue;
    }
    for (const c of e.causes) stack.push(c);
  }
  return missing;
}
