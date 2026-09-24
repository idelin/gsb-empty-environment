/**
 * Per-character identity model.
 *
 * A document version is an ordered array of code points, each carrying a
 * stable identity (`CharId`) that survives across versions. Edits produce a
 * new version plus a structural `Mapping` relating the two versions'
 * characters. Nothing here ever stores "final offsets" as the source of
 * truth: identity and relations are.
 */

export type CharId = number;
export type VersionId = number;

/** A single code point (never a lone surrogate half). */
export interface Char {
  id: CharId;
  ch: string;
}

export interface Version {
  id: VersionId;
  chars: Char[];
}

/** An edit in code-point coordinates. */
export interface Edit {
  start: number;
  deleteCount: number;
  insert?: string;
}

export interface IdAllocator {
  next: number;
}

import type { Mapping } from './mapping.ts';

export function toCodePoints(text: string): string[] {
  return Array.from(text);
}

export function makeVersion(id: VersionId, text: string, alloc: IdAllocator): Version {
  const chars = toCodePoints(text).map((ch) => ({ id: alloc.next++, ch }));
  return { id, chars };
}

export function textOf(version: Version): string {
  return version.chars.map((c) => c.ch).join('');
}

export function applyEdit(
  version: Version,
  id: VersionId,
  edit: Edit,
  alloc: IdAllocator,
): { version: Version; mapping: Mapping } {
  const { start, deleteCount } = edit;
  const insert = edit.insert ?? '';
  if (start < 0 || start > version.chars.length) {
    throw new RangeError(`edit start ${start} out of range`);
  }
  if (deleteCount < 0 || start + deleteCount > version.chars.length) {
    throw new RangeError(`edit deleteCount ${deleteCount} out of range`);
  }
  const oldChars = version.chars;
  const inserted: Char[] = toCodePoints(insert).map((ch) => ({ id: alloc.next++, ch }));
  const newChars = [
    ...oldChars.slice(0, start),
    ...inserted,
    ...oldChars.slice(start + deleteCount),
  ];

  const forward: number[] = new Array(oldChars.length).fill(-1);
  const backward: number[] = new Array(newChars.length).fill(-1);
  for (let i = 0; i < start; i++) {
    forward[i] = i;
    backward[i] = i;
  }
  const tailOld = start + deleteCount;
  const tailNew = start + inserted.length;
  for (let i = tailOld; i < oldChars.length; i++) {
    const j = tailNew + (i - tailOld);
    forward[i] = j;
    backward[j] = i;
  }

  return {
    version: { id, chars: newChars },
    mapping: { from: version.id, to: id, forward, backward },
  };
}
