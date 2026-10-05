import type { EventLog, Gap, LogEvent, SliceEdge, SliceResult, VarVersion } from './types.js';

export function varKey(v: VarVersion): string {
  return v.name + '@' + String(v.version);
}

export function sliceLog(log: EventLog, assertionId: number): SliceResult {
  const byId: Map<number, LogEvent> = new Map();
  const producers: Map<string, LogEvent> = new Map();
  for (const ev of log.events) {
    byId.set(ev.id, ev);
    if (ev.writes !== null) {
      producers.set(varKey(ev.writes), ev);
    }
  }

  const root = byId.get(assertionId);
  if (root === undefined) {
    throw new Error('no event with id ' + String(assertionId));
  }
  if (root.kind !== 'assert') {
    throw new Error('event #' + String(assertionId) + ' is not an assertion');
  }

  const included: Set<number> = new Set();
  const edges: SliceEdge[] = [];
  const gaps: Gap[] = [];
  const seenGaps: Set<string> = new Set();
  const queue: LogEvent[] = [root];

  while (queue.length > 0) {
    const ev = queue.pop();
    if (ev === undefined || included.has(ev.id)) {
      continue;
    }
    included.add(ev.id);

    for (const read of ev.reads) {
      const key = varKey(read);
      const producer = producers.get(key);
      if (producer === undefined) {
        const gapKey = 'producer:' + key + ':needed-by:' + String(ev.id);
        if (!seenGaps.has(gapKey)) {
          seenGaps.add(gapKey);
          gaps.push({
            kind: 'missing-producer',
            neededBy: ev.id,
            variable: read,
            controlId: null,
            message: 'event #' + String(ev.id) + ' reads ' + key + ' but no event in the log writes it',
          });
        }
      } else {
        edges.push({ from: ev.id, to: producer.id, kind: 'data', variable: read });
        if (!included.has(producer.id)) {
          queue.push(producer);
        }
      }
    }

    for (const ctrlId of ev.control) {
      const ctrl = byId.get(ctrlId);
      if (ctrl === undefined) {
        const gapKey = 'control:' + String(ctrlId) + ':needed-by:' + String(ev.id);
        if (!seenGaps.has(gapKey)) {
          seenGaps.add(gapKey);
          gaps.push({
            kind: 'missing-control',
            neededBy: ev.id,
            variable: null,
            controlId: ctrlId,
            message: 'event #' + String(ev.id) + ' is control-dependent on event #' + String(ctrlId) + ' which is absent from the log',
          });
        }
      } else {
        edges.push({ from: ev.id, to: ctrl.id, kind: 'control', variable: null });
        if (!included.has(ctrl.id)) {
          queue.push(ctrl);
        }
      }
    }
  }

  const events = log.events.filter((ev: LogEvent): boolean => included.has(ev.id));
  return { criterion: assertionId, events, edges, gaps };
}
