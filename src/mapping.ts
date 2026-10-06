// Segment mappings between two text buffers.
//
// A Segment maps a half-open output range [outStart, outEnd) to a half-open
// source range [srcStart, srcEnd). Two shapes exist:
//   - mapped:    srcEnd - srcStart === outEnd - outStart > 0 (copied/kept text)
//   - insertion: srcStart === srcEnd (zero-width anchor), outEnd > outStart
// Deleted source text is represented by absence: no segment covers it.
//
// A Mapping is a list of segments sorted by outStart and non-overlapping in
// output coordinates. Well-formed mappings produced by deriveFromLabels /
// compose tile the whole output buffer.

export interface Segment {
  outStart: number;
  outEnd: number;
  srcStart: number;
  srcEnd: number;
}

export interface SourceFragment {
  kind: 'source';
  srcStart: number;
  srcEnd: number;
}

export interface InsertionFragment {
  kind: 'insertion';
  anchor: number;
}

export type Fragment = SourceFragment | InsertionFragment;

export interface OutputRange {
  outStart: number;
  outEnd: number;
}

export function isInsertion(seg: Segment): boolean {
  return seg.srcStart === seg.srcEnd;
}

export function validateMapping(mapping: Segment[]): void {
  for (let i = 0; i < mapping.length; i++) {
    const s = mapping[i];
    if (!(s.outEnd > s.outStart)) {
      throw new Error('segment ' + i + ': output range must be non-empty');
    }
    if (!(s.srcEnd >= s.srcStart)) {
      throw new Error('segment ' + i + ': source range is inverted');
    }
    if (s.srcEnd > s.srcStart && s.srcEnd - s.srcStart !== s.outEnd - s.outStart) {
      throw new Error('segment ' + i + ': mapped ranges must have equal length');
    }
    if (i > 0 && s.outStart < mapping[i - 1].outEnd) {
      throw new Error('segment ' + i + ': segments overlap or are unsorted');
    }
  }
}

// Translate a zero-width position in the output of `mapping` back to a source
// position. Used to anchor insertions when composing.
export function anchorInSource(mapping: Segment[], pos: number): number {
  let left: Segment | null = null;
  for (const s of mapping) {
    if (pos >= s.outStart && pos < s.outEnd) {
      return isInsertion(s) ? s.srcStart : s.srcStart + (pos - s.outStart);
    }
    if (s.outEnd <= pos) {
      left = s;
    }
    if (s.outStart > pos) {
      return left ? left.srcEnd : s.srcStart;
    }
  }
  return left ? left.srcEnd : 0;
}

// Compose `first` (S0 -> S1) with `second` (S1 -> S2), yielding S0 -> S2.
export function compose(first: Segment[], second: Segment[]): Segment[] {
  const out: Segment[] = [];
  for (const b of second) {
    if (isInsertion(b)) {
      const anchor = anchorInSource(first, b.srcStart);
      pushSegment(out, { outStart: b.outStart, outEnd: b.outEnd, srcStart: anchor, srcEnd: anchor });
      continue;
    }
    for (const a of first) {
      if (a.outEnd <= b.srcStart) continue;
      if (a.outStart >= b.srcEnd) break;
      const lo = Math.max(a.outStart, b.srcStart);
      const hi = Math.min(a.outEnd, b.srcEnd);
      if (lo >= hi) continue;
      const outLo = b.outStart + (lo - b.srcStart);
      const outHi = b.outStart + (hi - b.srcStart);
      if (isInsertion(a)) {
        pushSegment(out, { outStart: outLo, outEnd: outHi, srcStart: a.srcStart, srcEnd: a.srcStart });
      } else {
        const srcLo = a.srcStart + (lo - a.outStart);
        const srcHi = a.srcStart + (hi - a.outStart);
        pushSegment(out, { outStart: outLo, outEnd: outHi, srcStart: srcLo, srcEnd: srcHi });
      }
    }
  }
  return out;
}

function pushSegment(out: Segment[], seg: Segment): void {
  const last = out[out.length - 1];
  if (last && last.outEnd === seg.outStart) {
    if (isInsertion(last) && isInsertion(seg) && last.srcStart === seg.srcStart) {
      last.outEnd = seg.outEnd;
      return;
    }
    if (!isInsertion(last) && !isInsertion(seg) && last.srcEnd === seg.srcStart) {
      last.outEnd = seg.outEnd;
      last.srcEnd = seg.srcEnd;
      return;
    }
  }
  out.push(seg);
}

// Forward query: which source fragments produced the output range
// [qStart, qEnd)? Fragments come back in output order. Adjacent source
// fragments are merged only when they are contiguous in the source, so a
// deletion gap keeps them apart; zero-width insertions do not block merging.
export function querySource(mapping: Segment[], qStart: number, qEnd: number): Fragment[] {
  const fragments: Fragment[] = [];
  let lastSource: SourceFragment | null = null;
  for (const s of mapping) {
    if (s.outEnd <= qStart) continue;
    if (s.outStart >= qEnd) break;
    const lo = Math.max(s.outStart, qStart);
    const hi = Math.min(s.outEnd, qEnd);
    if (isInsertion(s)) {
      fragments.push({ kind: 'insertion', anchor: s.srcStart });
      continue;
    }
    const srcLo = s.srcStart + (lo - s.outStart);
    const srcHi = s.srcStart + (hi - s.outStart);
    if (lastSource && lastSource.srcEnd === srcLo) {
      lastSource.srcEnd = srcHi;
    } else {
      const frag: SourceFragment = { kind: 'source', srcStart: srcLo, srcEnd: srcHi };
      fragments.push(frag);
      lastSource = frag;
    }
  }
  return fragments;
}

// Reverse query: where does the source range [sStart, sEnd) appear in the
// output? Multi-valued: copied source text yields one range per occurrence.
// Insertions are not source text and never appear in the result.
export function queryOutputs(mapping: Segment[], sStart: number, sEnd: number): OutputRange[] {
  const ranges: OutputRange[] = [];
  for (const s of mapping) {
    if (isInsertion(s)) continue;
    const lo = Math.max(s.srcStart, sStart);
    const hi = Math.min(s.srcEnd, sEnd);
    if (lo >= hi) continue;
    ranges.push({ outStart: s.outStart + (lo - s.srcStart), outEnd: s.outStart + (hi - s.srcStart) });
  }
  ranges.sort((x, y) => x.outStart - y.outStart);
  return ranges;
}
