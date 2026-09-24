import type { Edit, Range } from './types.ts';

/**
 * Independent step-by-step reference mapper used to cross-check the
 * composed mappings produced by History. It deliberately shares no code
 * with the History/marker machinery: each edit is applied to the range
 * with plain offset arithmetic, one version at a time.
 */
export function referenceMapRange(edits: Edit[], range: Range): Range | null {
  let current: Range | null = range;
  for (const edit of edits) {
    if (current === null) return null;
    current = mapOne(edit, current);
  }
  return current;
}

function mapOne(edit: Edit, range: Range): Range | null {
  switch (edit.kind) {
    case 'insert':
      return mapInsert(edit.at, edit.text.length, range);
    case 'delete':
      return mapDelete(edit.start, edit.end, range);
    case 'replace': {
      const afterDelete = mapDelete(edit.start, edit.end, range);
      if (afterDelete === null) return null;
      return mapInsert(edit.start, edit.text.length, afterDelete);
    }
    case 'move': {
      const d = edit.end - edit.start;
      if (
        edit.moveId !== undefined &&
        range.start >= edit.start &&
        range.end <= edit.end
      ) {
        return {
          start: edit.to + (range.start - edit.start),
          end: edit.to + (range.end - edit.start),
        };
      }
      const afterDelete = mapDelete(edit.start, edit.end, range);
      if (afterDelete === null) return null;
      return mapInsert(edit.to, d, afterDelete);
    }
  }
}

function mapInsert(at: number, length: number, range: Range): Range {
  if (at <= range.start) {
    return { start: range.start + length, end: range.end + length };
  }
  if (at >= range.end) return range;
  return { start: range.start, end: range.end + length };
}

function mapDelete(start: number, end: number, range: Range): Range | null {
  const d = end - start;
  if (end <= range.start) {
    return { start: range.start - d, end: range.end - d };
  }
  if (start >= range.end) return range;
  if (start <= range.start && end >= range.end) return null;
  if (start <= range.start) return { start, end: range.end - d };
  if (end >= range.end) return { start: range.start, end: start };
  return { start: range.start, end: range.end - d };
}
