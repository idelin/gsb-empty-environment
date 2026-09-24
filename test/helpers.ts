import type { Edit } from '../src/edits.ts';

export interface RefRangeResult {
  status: 'intact' | 'shrank' | 'deleted';
  start: number;
  end: number;
  deletedUnits: number;
}

export function referenceMapRange(
  text: string,
  edits: Edit[],
  start: number,
  end: number,
): RefRangeResult {
  const tokens: number[] = Array.from({ length: text.length }, (_, i) => i);
  for (const e of edits) {
    if (e.kind === 'insert') {
      tokens.splice(e.offset, 0, ...Array<number>(e.text.length).fill(-1));
    } else if (e.kind === 'delete') {
      tokens.splice(e.start, e.end - e.start);
    } else {
      const block = tokens.splice(e.start, e.end - e.start);
      tokens.splice(e.to, 0, ...block);
    }
  }
  const surviving: number[] = [];
  for (let i = 0; i < tokens.length; i++) {
    const t = tokens[i]!;
    if (t >= start && t < end) surviving.push(i);
  }
  if (surviving.length === 0) {
    return { status: 'deleted', start: 0, end: 0, deletedUnits: end - start };
  }
  const deletedUnits = end - start - surviving.length;
  return {
    status: deletedUnits > 0 ? 'shrank' : 'intact',
    start: surviving[0]!,
    end: surviving[surviving.length - 1]! + 1,
    deletedUnits,
  };
}
