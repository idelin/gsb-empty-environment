import type { EventLog, LogEvent, VarVersion } from './model.ts';
import { varKey } from './model.ts';

export type SliceGap =
  | { type: 'missing-producer'; ref: VarVersion; neededBy: number }
  | { type: 'missing-control-event'; eventId: number; neededBy: number };

export interface SliceResult {
  criterionId: number;
  events: LogEvent[];
  gaps: SliceGap[];
}

type WorkItem =
  | { kind: 'read'; ref: VarVersion; neededBy: number }
  | { kind: 'control'; eventId: number; neededBy: number };

export function sliceLog(log: EventLog, criterionId: number): SliceResult {
  const eventsById = new Map<number, LogEvent>();
  const writesByVar = new Map<string, LogEvent & { kind: 'write' }>();
  for (const event of log.events) {
    eventsById.set(event.id, event);
    if (event.kind === 'write') writesByVar.set(varKey(event.target), event);
  }

  const criterion = eventsById.get(criterionId);
  if (!criterion) throw new Error(`criterion event ${criterionId} not found in log`);

  const included = new Set<number>();
  const gaps: SliceGap[] = [];
  const seenGaps = new Set<string>();
  const worklist: WorkItem[] = [];

  const pushGap = (gap: SliceGap): void => {
    const key =
      gap.type === 'missing-producer'
        ? `producer:${varKey(gap.ref)}:${gap.neededBy}`
        : `control:${gap.eventId}:${gap.neededBy}`;
    if (!seenGaps.has(key)) {
      seenGaps.add(key);
      gaps.push(gap);
    }
  };

  const enqueueEvent = (event: LogEvent): void => {
    if (included.has(event.id)) return;
    included.add(event.id);
    for (const ref of event.reads) worklist.push({ kind: 'read', ref, neededBy: event.id });
    for (const eventId of event.control) worklist.push({ kind: 'control', eventId, neededBy: event.id });
  };

  enqueueEvent(criterion);

  while (worklist.length > 0) {
    const item = worklist.pop()!;
    if (item.kind === 'read') {
      const producer = writesByVar.get(varKey(item.ref));
      if (!producer) {
        pushGap({ type: 'missing-producer', ref: item.ref, neededBy: item.neededBy });
        continue;
      }
      enqueueEvent(producer);
    } else {
      const controller = eventsById.get(item.eventId);
      if (!controller) {
        pushGap({ type: 'missing-control-event', eventId: item.eventId, neededBy: item.neededBy });
        continue;
      }
      enqueueEvent(controller);
    }
  }

  const events = log.events.filter((e) => included.has(e.id));
  return { criterionId, events, gaps };
}
