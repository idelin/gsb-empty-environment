import type { Edit } from './edits.ts';

export type Segment =
  | { kind: 'kept'; oldStart: number; oldEnd: number; newStart: number }
  | { kind: 'deleted'; oldStart: number; oldEnd: number };

export interface SegmentMapping {
  oldLen: number;
  newLen: number;
  segments: Segment[];
}

export type Affinity = 'left' | 'right';

export interface RangeMapResult {
  status: 'intact' | 'shrank' | 'deleted';
  start: number;
  end: number;
  deletedUnits: number;
}

export function identityMapping(len: number): SegmentMapping {
  const segments: Segment[] =
    len === 0 ? [] : [{ kind: 'kept', oldStart: 0, oldEnd: len, newStart: 0 }];
  return { oldLen: len, newLen: len, segments };
}

function pushKept(segments: Segment[], oldStart: number, oldEnd: number, newStart: number): void {
  if (oldEnd > oldStart) segments.push({ kind: 'kept', oldStart, oldEnd, newStart });
}

function pushDeleted(segments: Segment[], oldStart: number, oldEnd: number): void {
  if (oldEnd > oldStart) segments.push({ kind: 'deleted', oldStart, oldEnd });
}

export function mappingFromEdit(oldLen: number, edit: Edit): SegmentMapping {
  const segments: Segment[] = [];
  switch (edit.kind) {
    case 'insert': {
      pushKept(segments, 0, edit.offset, 0);
      pushKept(segments, edit.offset, oldLen, edit.offset + edit.text.length);
      return normalize({ oldLen, newLen: oldLen + edit.text.length, segments });
    }
    case 'delete': {
      pushKept(segments, 0, edit.start, 0);
      pushDeleted(segments, edit.start, edit.end);
      pushKept(segments, edit.end, oldLen, edit.start);
      return normalize({ oldLen, newLen: oldLen - (edit.end - edit.start), segments });
    }
    case 'move': {
      const { start, end, to } = edit;
      const len = end - start;
      if (to <= start) {
        pushKept(segments, 0, to, 0);
        pushKept(segments, to, start, to + len);
        pushKept(segments, start, end, to);
        pushKept(segments, end, oldLen, start + len);
      } else {
        pushKept(segments, 0, start, 0);
        pushKept(segments, end, end + (to - start), start);
        pushKept(segments, start, end, to);
        pushKept(segments, end + (to - start), oldLen, to + len);
      }
      return normalize({ oldLen, newLen: oldLen, segments });
    }
  }
}

export function normalize(mapping: SegmentMapping): SegmentMapping {
  const sorted = [...mapping.segments].sort((a, b) => a.oldStart - b.oldStart);
  const merged: Segment[] = [];
  for (const seg of sorted) {
    const last = merged[merged.length - 1];
    if (last && last.oldEnd === seg.oldStart) {
      if (last.kind === 'deleted' && seg.kind === 'deleted') {
        last.oldEnd = seg.oldEnd;
        continue;
      }
      if (
        last.kind === 'kept' &&
        seg.kind === 'kept' &&
        last.newStart - last.oldStart === seg.newStart - seg.oldStart
      ) {
        last.oldEnd = seg.oldEnd;
        continue;
      }
    }
    merged.push({ ...seg });
  }
  return { oldLen: mapping.oldLen, newLen: mapping.newLen, segments: merged };
}

export function composeMappings(first: SegmentMapping, second: SegmentMapping): SegmentMapping {
  if (first.newLen !== second.oldLen) {
    throw new Error(
      'mapping length mismatch: first maps to ' + first.newLen + ', second expects ' + second.oldLen,
    );
  }
  const segments: Segment[] = [];
  for (const s1 of first.segments) {
    if (s1.kind === 'deleted') {
      segments.push({ ...s1 });
      continue;
    }
    const imgStart = s1.newStart;
    const imgEnd = imgStart + (s1.oldEnd - s1.oldStart);
    for (const s2 of second.segments) {
      if (s2.oldEnd <= imgStart) continue;
      if (s2.oldStart >= imgEnd) break;
      const a = Math.max(imgStart, s2.oldStart);
      const b = Math.min(imgEnd, s2.oldEnd);
      const oldA = s1.oldStart + (a - imgStart);
      const oldB = s1.oldStart + (b - imgStart);
      if (s2.kind === 'kept') {
        segments.push({ kind: 'kept', oldStart: oldA, oldEnd: oldB, newStart: s2.newStart + (a - s2.oldStart) });
      } else {
        segments.push({ kind: 'deleted', oldStart: oldA, oldEnd: oldB });
      }
    }
  }
  return normalize({ oldLen: first.oldLen, newLen: second.newLen, segments });
}

export function invertMapping(mapping: SegmentMapping): SegmentMapping {
  const kept = mapping.segments
    .filter((s): s is Extract<Segment, { kind: 'kept' }> => s.kind === 'kept')
    .sort((a, b) => a.newStart - b.newStart);
  const segments: Segment[] = [];
  let cursor = 0;
  for (const s of kept) {
    const len = s.oldEnd - s.oldStart;
    if (s.newStart > cursor) pushDeleted(segments, cursor, s.newStart);
    segments.push({ kind: 'kept', oldStart: s.newStart, oldEnd: s.newStart + len, newStart: s.oldStart });
    cursor = s.newStart + len;
  }
  if (cursor < mapping.newLen) pushDeleted(segments, cursor, mapping.newLen);
  return normalize({ oldLen: mapping.newLen, newLen: mapping.oldLen, segments });
}

export function mapOffset(
  mapping: SegmentMapping,
  offset: number,
  affinity: Affinity = 'right',
): number | null {
  if (!Number.isInteger(offset) || offset < 0 || offset > mapping.oldLen) {
    throw new RangeError('offset ' + offset + ' out of range [0, ' + mapping.oldLen + ']');
  }
  const segs = mapping.segments;
  for (let i = 0; i < segs.length; i++) {
    const s = segs[i]!;
    if (offset < s.oldStart) return null;
    if (offset > s.oldStart && offset < s.oldEnd) {
      return s.kind === 'kept' ? s.newStart + (offset - s.oldStart) : null;
    }
    if (offset === s.oldStart) {
      const prev = i > 0 ? segs[i - 1]! : null;
      if (affinity === 'left' && prev && prev.oldEnd === offset) {
        return prev.kind === 'kept' ? prev.newStart + (prev.oldEnd - prev.oldStart) : null;
      }
      return s.kind === 'kept' ? s.newStart : null;
    }
  }
  const last = segs[segs.length - 1];
  if (last && last.oldEnd === offset) {
    return last.kind === 'kept' ? last.newStart + (last.oldEnd - last.oldStart) : null;
  }
  return null;
}

export function mapRange(mapping: SegmentMapping, start: number, end: number): RangeMapResult {
  if (start > end) throw new RangeError('range start > range end');
  if (start === end) {
    const point = mapOffset(mapping, start, 'right');
    if (point === null) return { status: 'deleted', start: 0, end: 0, deletedUnits: 0 };
    return { status: 'intact', start: point, end: point, deletedUnits: 0 };
  }
  let newStart = Number.POSITIVE_INFINITY;
  let newEnd = Number.NEGATIVE_INFINITY;
  let deletedUnits = 0;
  for (const s of mapping.segments) {
    if (s.oldEnd <= start) continue;
    if (s.oldStart >= end) break;
    if (s.kind === 'kept') {
      const a = Math.max(start, s.oldStart);
      const b = Math.min(end, s.oldEnd);
      newStart = Math.min(newStart, s.newStart + (a - s.oldStart));
      newEnd = Math.max(newEnd, s.newStart + (b - s.oldStart));
    } else {
      deletedUnits += Math.min(end, s.oldEnd) - Math.max(start, s.oldStart);
    }
  }
  if (newEnd === Number.NEGATIVE_INFINITY) {
    return { status: 'deleted', start: 0, end: 0, deletedUnits: end - start };
  }
  return {
    status: deletedUnits > 0 ? 'shrank' : 'intact',
    start: newStart,
    end: newEnd,
    deletedUnits,
  };
}
