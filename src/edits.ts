import { assertUtf16Boundary } from './utf16.ts';
import type { Edit, Range } from './types.ts';

function checkRange(text: string, start: number, end: number, what: string): void {
  assertUtf16Boundary(text, start, `${what} start`);
  assertUtf16Boundary(text, end, `${what} end`);
  if (start > end) {
    throw new RangeError(`${what}: start ${start} > end ${end}`);
  }
}

/** Apply one edit to a document string, returning the new string. */
export function applyEditToText(text: string, edit: Edit): string {
  switch (edit.kind) {
    case 'insert': {
      assertUtf16Boundary(text, edit.at, 'insert');
      return text.slice(0, edit.at) + edit.text + text.slice(edit.at);
    }
    case 'delete': {
      checkRange(text, edit.start, edit.end, 'delete');
      return text.slice(0, edit.start) + text.slice(edit.end);
    }
    case 'replace': {
      checkRange(text, edit.start, edit.end, 'replace');
      return text.slice(0, edit.start) + edit.text + text.slice(edit.end);
    }
    case 'move': {
      checkRange(text, edit.start, edit.end, 'move');
      const block = text.slice(edit.start, edit.end);
      const rest = text.slice(0, edit.start) + text.slice(edit.end);
      assertUtf16Boundary(rest, edit.to, 'move destination');
      return rest.slice(0, edit.to) + block + rest.slice(edit.to);
    }
  }
}

export type TransformKind =
  | 'unchanged'
  | 'shift'
  | 'expand'
  | 'shrink'
  | 'move-follow';

export type RangeTransform =
  | { status: 'mapped'; range: Range; kind: TransformKind; detail: string }
  | { status: 'deleted'; detail: string };

function mapped(range: Range, kind: TransformKind, detail: string): RangeTransform {
  return { status: 'mapped', range, kind, detail };
}

function deleted(detail: string): RangeTransform {
  return { status: 'deleted', detail };
}

function transformThroughInsert(range: Range, at: number, length: number): RangeTransform {
  const { start, end } = range;
  if (at <= start) {
    return mapped(
      { start: start + length, end: end + length },
      'shift',
      `insert of ${length} unit(s) at ${at} shifts range right`,
    );
  }
  if (at >= end) {
    return mapped(range, 'unchanged', `insert at ${at} is after the range`);
  }
  return mapped(
    { start, end: end + length },
    'expand',
    `insert at ${at} lands inside the range`,
  );
}

function transformThroughDelete(range: Range, delStart: number, delEnd: number): RangeTransform {
  const { start, end } = range;
  const d = delEnd - delStart;
  if (delEnd <= start) {
    return mapped(
      { start: start - d, end: end - d },
      'shift',
      `delete of [${delStart}, ${delEnd}) shifts range left`,
    );
  }
  if (delStart >= end) {
    return mapped(range, 'unchanged', `delete of [${delStart}, ${delEnd}) is after the range`);
  }
  if (delStart <= start && delEnd >= end) {
    return deleted(`delete of [${delStart}, ${delEnd}) fully covers the range`);
  }
  if (delStart <= start) {
    return mapped(
      { start: delStart, end: end - d },
      'shrink',
      `delete of [${delStart}, ${delEnd}) trims the head of the range`,
    );
  }
  if (delEnd >= end) {
    return mapped(
      { start, end: delStart },
      'shrink',
      `delete of [${delStart}, ${delEnd}) trims the tail of the range`,
    );
  }
  return mapped(
    { start, end: end - d },
    'shrink',
    `delete of [${delStart}, ${delEnd}) removes the middle of the range`,
  );
}

/**
 * Map a range forward through one edit. Identity is preserved across a
 * `move` only when the edit carries a stable `moveId` and the range is
 * fully inside the moved block; otherwise the move degrades to
 * delete + insert semantics.
 */
export function transformRange(range: Range, edit: Edit): RangeTransform {
  switch (edit.kind) {
    case 'insert':
      return transformThroughInsert(range, edit.at, edit.text.length);
    case 'delete':
      return transformThroughDelete(range, edit.start, edit.end);
    case 'replace': {
      const afterDelete = transformThroughDelete(range, edit.start, edit.end);
      if (afterDelete.status === 'deleted') return afterDelete;
      const afterInsert = transformThroughInsert(afterDelete.range, edit.start, edit.text.length);
      if (afterInsert.status === 'deleted') return afterInsert;
      const kind = afterDelete.kind === 'unchanged' ? afterInsert.kind : afterDelete.kind;
      return mapped(afterInsert.range, kind, `replace: ${afterDelete.detail}; ${afterInsert.detail}`);
    }
    case 'move': {
      const { start, end, to, moveId } = edit;
      const d = end - start;
      if (moveId !== undefined && range.start >= start && range.end <= end) {
        return mapped(
          { start: to + (range.start - start), end: to + (range.end - start) },
          'move-follow',
          `followed stable move "${moveId}" of [${start}, ${end}) to ${to}`,
        );
      }
      const afterDelete = transformThroughDelete(range, start, end);
      if (afterDelete.status === 'deleted') {
        return deleted(
          moveId === undefined
            ? `move without a stable id is delete + insert: ${afterDelete.detail}`
            : afterDelete.detail,
        );
      }
      const afterInsert = transformThroughInsert(afterDelete.range, to, d);
      if (afterInsert.status === 'deleted') return afterInsert;
      const kind = afterDelete.kind === 'unchanged' ? afterInsert.kind : afterDelete.kind;
      return mapped(afterInsert.range, kind, `move: ${afterDelete.detail}; ${afterInsert.detail}`);
    }
  }
}

/**
 * Map a range backward through one edit (from the edited document to the
 * original). A range fully inside inserted text has no origin and is
 * reported as deleted.
 */
export function transformRangeBackward(range: Range, edit: Edit): RangeTransform {
  switch (edit.kind) {
    case 'insert': {
      const p = edit.at;
      const len = edit.text.length;
      const { start, end } = range;
      if (end <= p) return mapped(range, 'unchanged', `before insert at ${p}`);
      if (start >= p + len) {
        return mapped({ start: start - len, end: end - len }, 'shift', `after insert at ${p}`);
      }
      if (start >= p && end <= p + len) {
        return deleted('range lies entirely inside inserted text; no origin');
      }
      const newStart = start < p ? start : p;
      const newEnd = end > p + len ? end - len : p;
      return mapped({ start: newStart, end: newEnd }, 'shrink', `range crosses insert at ${p}`);
    }
    case 'delete': {
      const d = edit.end - edit.start;
      const shift = (o: number): number => (o < edit.start ? o : o + d);
      return mapped(
        { start: shift(range.start), end: shift(range.end) },
        'shift',
        `backward across delete of [${edit.start}, ${edit.end})`,
      );
    }
    case 'replace': {
      // Forward replace = delete then insert; undo insert first, then delete.
      const undoInsert = transformRangeBackward(range, {
        kind: 'insert',
        at: edit.start,
        text: edit.text,
      });
      if (undoInsert.status === 'deleted') return undoInsert;
      return transformRangeBackward(undoInsert.range, {
        kind: 'delete',
        start: edit.start,
        end: edit.end,
      });
    }
    case 'move': {
      // Inverse of move [start,end) -> to is move [to, to+d) -> start.
      const d = edit.end - edit.start;
      return transformRange(range, {
        kind: 'move',
        start: edit.to,
        end: edit.to + d,
        to: edit.start,
        moveId: edit.moveId,
      });
    }
  }
}
