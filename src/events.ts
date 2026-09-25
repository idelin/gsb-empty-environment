import type { Ticks } from './time.ts';

export interface TimelineEvent {
  name: string;
  /** Event time in the LOCAL frame of the node that owns it. */
  at: Ticks;
  /**
   * Version of the source (animation / timeline) when the event was
   * generated. After a hot-swap the source version bumps and events carrying
   * an older version are invalid.
   */
  version: number;
  /** Path of the owning node, e.g. "root/group/fade". */
  path: string;
  property?: string;
}

/**
 * Crossing-interval semantics.
 *
 * Advancing playback from `from` to `to` delivers events whose time lies in
 * the half-open interval oriented by the direction of travel:
 *   forward  (to > from):  (from, to]
 *   backward (to < from):  [to, from)
 *   still    (to == from): nothing
 *
 * Consecutive intervals tile the timeline exactly, so an event sitting on a
 * boundary is delivered exactly once and is never re-fired by the next step.
 */
export function inCrossingInterval(at: Ticks, from: Ticks, to: Ticks): boolean {
  if (to > from) return at > from && at <= to;
  if (to < from) return at >= to && at < from;
  return false;
}

/** Stable delivery order: ascending when moving forward, descending backward. */
export function sortForDelivery<T extends { at: Ticks; name: string; path: string }>(
  events: T[],
  from: Ticks,
  to: Ticks,
): T[] {
  const dir = to >= from ? 1 : -1;
  return events.slice().sort((a, b) => {
    if (a.at !== b.at) return (a.at - b.at) * dir;
    if (a.path !== b.path) return a.path < b.path ? -1 : 1;
    return a.name < b.name ? -1 : a.name > b.name ? 1 : 0;
  });
}
