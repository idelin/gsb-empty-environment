import { assertUtf16Boundary } from './utf16.ts';

export type Edit =
  | { kind: 'insert'; id: number; offset: number; text: string }
  | { kind: 'delete'; id: number; start: number; end: number }
  | { kind: 'move'; id: number; start: number; end: number; to: number; stableId: string };

export function validateEdit(doc: string, edit: Edit): void {
  switch (edit.kind) {
    case 'insert':
      assertUtf16Boundary(doc, edit.offset, 'insert offset');
      break;
    case 'delete':
      assertUtf16Boundary(doc, edit.start, 'delete start');
      assertUtf16Boundary(doc, edit.end, 'delete end');
      if (edit.start > edit.end) throw new RangeError('delete start > delete end');
      break;
    case 'move': {
      assertUtf16Boundary(doc, edit.start, 'move start');
      assertUtf16Boundary(doc, edit.end, 'move end');
      if (edit.start >= edit.end) throw new RangeError('move source is empty');
      const maxTo = doc.length - (edit.end - edit.start);
      if (!Number.isInteger(edit.to) || edit.to < 0 || edit.to > maxTo) {
        throw new RangeError('move target ' + edit.to + ' out of range [0, ' + maxTo + ']');
      }
      const removed = doc.slice(0, edit.start) + doc.slice(edit.end);
      assertUtf16Boundary(removed, edit.to, 'move target');
      break;
    }
  }
}

export function applyEdit(doc: string, edit: Edit): string {
  validateEdit(doc, edit);
  switch (edit.kind) {
    case 'insert':
      return doc.slice(0, edit.offset) + edit.text + doc.slice(edit.offset);
    case 'delete':
      return doc.slice(0, edit.start) + doc.slice(edit.end);
    case 'move': {
      const block = doc.slice(edit.start, edit.end);
      const removed = doc.slice(0, edit.start) + doc.slice(edit.end);
      return removed.slice(0, edit.to) + block + removed.slice(edit.to);
    }
  }
}
