import { deepDiff, eventView } from './compare.js';
import { SegmentCursor, countRemainingObservables, totalEvents } from './cursor.js';
import type { ObservablePred } from './cursor.js';
import type {
  CompareOptions,
  DiffReport,
  Divergence,
  Segment,
  SideStats,
  TraceEvent,
  TraceInput,
  ValueDiff,
  WindowSide,
} from './types.js';

interface NormalizedOptions {
  observable: Set<string>;
  ignore: Set<string>;
  tolerance: number;
  maxWindow: number;
  lookahead: number;
  maxValueDiffs: number;
}

function normalizeOptions(options: CompareOptions | undefined): NormalizedOptions {
  if (options === undefined || options === null) {
    throw new Error('options are required (at least options.observable)');
  }
  if (!Array.isArray(options.observable)) {
    throw new Error('options.observable must be an array of observable action names');
  }
  for (const a of options.observable) {
    if (typeof a !== 'string') throw new Error('options.observable must contain only strings');
  }
  return {
    observable: new Set(options.observable),
    ignore: new Set(options.ignoreKeys === undefined ? [] : options.ignoreKeys),
    tolerance: options.numericTolerance === undefined ? 0 : options.numericTolerance,
    maxWindow: options.maxWindow === undefined ? 20 : options.maxWindow,
    lookahead: options.lookahead === undefined ? 10 : options.lookahead,
    maxValueDiffs: options.maxValueDiffs === undefined ? 50 : options.maxValueDiffs,
  };
}

function isTraceEvent(x: unknown): x is TraceEvent {
  if (typeof x !== 'object' || x === null) return false;
  if (!('action' in x)) return false;
  return typeof x.action === 'string';
}

function isSegment(x: unknown): x is Segment {
  if (typeof x !== 'object' || x === null || Array.isArray(x)) return false;
  if (!('kind' in x)) return false;
  if (x.kind === 'run') return 'events' in x && Array.isArray(x.events);
  if (x.kind === 'loop') {
    return 'body' in x && Array.isArray(x.body) && 'times' in x && typeof x.times === 'number';
  }
  return false;
}

function validateSegments(segments: Segment[]): Segment[] {
  for (const s of segments) {
    if (s.kind === 'loop') {
      if (!Number.isInteger(s.times) || s.times < 0) {
        throw new Error('loop segment requires an integer "times" >= 0');
      }
      for (const e of s.body) {
        if (!isTraceEvent(e)) throw new Error('loop body contains a non-event entry');
      }
    } else {
      for (const e of s.events) {
        if (!isTraceEvent(e)) throw new Error('run segment contains a non-event entry');
      }
    }
  }
  return segments;
}

export function normalizeSegments(input: TraceInput): Segment[] {
  const raw: unknown = input;
  if (Array.isArray(raw)) {
    if (raw.length === 0) return [{ kind: 'run', events: [] }];
    if (raw.every(isSegment)) return validateSegments(raw);
    if (raw.every(isTraceEvent)) return [{ kind: 'run', events: raw }];
    throw new Error('trace array must contain either all events or all segments, not a mix');
  }
  if (typeof raw === 'object' && raw !== null) {
    if ('segments' in raw) {
      const segs: unknown = raw.segments;
      if (Array.isArray(segs) && segs.every(isSegment)) return validateSegments(segs);
      throw new Error('"segments" must be an array of segments');
    }
    if ('events' in raw) {
      const events: unknown = raw.events;
      if (Array.isArray(events) && events.every(isTraceEvent)) return [{ kind: 'run', events: events }];
      throw new Error('"events" must be an array of trace events');
    }
  }
  throw new Error('invalid trace input: expected an event array, a segment array, or { segments } / { events }');
}

function scanAhead(cursor: SegmentCursor, target: string, limit: number, pred: ObservablePred): number | null {
  const clone = cursor.clone();
  clone.next();
  const scratch: TraceEvent[] = [];
  for (let offset = 0; offset < limit; offset++) {
    clone.skipInternal(pred, scratch, 0);
    const e = clone.peek();
    if (e === null) return null;
    if (e.action === target) return offset;
    clone.next();
  }
  return null;
}

function windowSide(
  lastMatched: TraceEvent | null,
  gap: TraceEvent[],
  gapTotal: number,
  diverging: TraceEvent | null
): WindowSide {
  return {
    lastMatchedObservable: lastMatched,
    internal: gap,
    internalTotal: gapTotal,
    truncated: gapTotal > gap.length,
    divergingObservable: diverging,
  };
}

function buildStats(
  leftSegs: Segment[],
  rightSegs: Segment[],
  lc: SegmentCursor,
  rc: SegmentCursor,
  internalLeft: number,
  internalRight: number,
  matched: number
): { left: SideStats; right: SideStats } {
  return {
    left: {
      totalEvents: totalEvents(leftSegs),
      consumedEvents: lc.consumed,
      internalEvents: internalLeft,
      matchedObservables: matched,
    },
    right: {
      totalEvents: totalEvents(rightSegs),
      consumedEvents: rc.consumed,
      internalEvents: internalRight,
      matchedObservables: matched,
    },
  };
}

export function diffTrajectories(
  left: TraceInput,
  right: TraceInput,
  options: CompareOptions | undefined
): DiffReport {
  const opts = normalizeOptions(options);
  const leftSegs = normalizeSegments(left);
  const rightSegs = normalizeSegments(right);
  const pred: ObservablePred = (action: string): boolean => opts.observable.has(action);
  const lc = new SegmentCursor(leftSegs);
  const rc = new SegmentCursor(rightSegs);
  let matched = 0;
  let internalLeft = 0;
  let internalRight = 0;
  let lastMatchedLeft: TraceEvent | null = null;
  let lastMatchedRight: TraceEvent | null = null;

  for (;;) {
    const gapLeft: TraceEvent[] = [];
    const gapRight: TraceEvent[] = [];
    const gapLeftTotal = lc.skipInternal(pred, gapLeft, opts.maxWindow);
    const gapRightTotal = rc.skipInternal(pred, gapRight, opts.maxWindow);
    internalLeft += gapLeftTotal;
    internalRight += gapRightTotal;
    const le = lc.peek();
    const re = rc.peek();

    if (le === null && re === null) {
      return {
        equal: true,
        matchedObservables: matched,
        divergence: null,
        stats: buildStats(leftSegs, rightSegs, lc, rc, internalLeft, internalRight, matched),
      };
    }

    const window = {
      left: windowSide(lastMatchedLeft, gapLeft, gapLeftTotal, le),
      right: windowSide(lastMatchedRight, gapRight, gapRightTotal, re),
    };

    if (le === null || re === null) {
      const presentCursor = le === null ? rc : lc;
      const presentAction = le !== null ? le.action : re !== null ? re.action : '';
      const remaining = countRemainingObservables(presentCursor, pred);
      const endedSide = le === null ? 'left' : 'right';
      const otherSide = le === null ? 'right' : 'left';
      const divergence: Divergence = {
        kind: 'missing-action',
        observableIndex: matched,
        left: le,
        right: re,
        valueDiffs: [],
        valueDiffsTruncated: false,
        foundInLeftAt: null,
        foundInRightAt: null,
        remainingObservables: remaining,
        detail:
          endedSide +
          ' trace ended after ' +
          matched +
          ' matched observable(s); ' +
          otherSide +
          ' has ' +
          remaining +
          ' more observable action(s), next: "' +
          presentAction +
          '"',
        window: window,
      };
      return {
        equal: false,
        matchedObservables: matched,
        divergence: divergence,
        stats: buildStats(leftSegs, rightSegs, lc, rc, internalLeft, internalRight, matched),
      };
    }

    if (le.action !== re.action) {
      const foundInLeft = scanAhead(lc, re.action, opts.lookahead, pred);
      const foundInRight = scanAhead(rc, le.action, opts.lookahead, pred);
      let detail =
        'order mismatch at observable #' +
        matched +
        ': left next "' +
        le.action +
        '", right next "' +
        re.action +
        '"';
      if (foundInLeft !== null && foundInRight !== null) {
        detail +=
          '; actions reordered ("' +
          le.action +
          '" appears at +' +
          foundInRight +
          ' in right, "' +
          re.action +
          '" at +' +
          foundInLeft +
          ' in left)';
      } else if (foundInLeft !== null) {
        detail +=
          '; "' +
          re.action +
          '" appears at +' +
          foundInLeft +
          ' in left, but "' +
          le.action +
          '" not found within right\'s next ' +
          opts.lookahead +
          ' observable(s)';
      } else if (foundInRight !== null) {
        detail +=
          '; "' +
          le.action +
          '" appears at +' +
          foundInRight +
          ' in right, but "' +
          re.action +
          '" not found within left\'s next ' +
          opts.lookahead +
          ' observable(s)';
      } else {
        detail += '; neither action appears within the other trace\'s next ' + opts.lookahead + ' observable(s)';
      }
      const divergence: Divergence = {
        kind: 'order-mismatch',
        observableIndex: matched,
        left: le,
        right: re,
        valueDiffs: [],
        valueDiffsTruncated: false,
        foundInLeftAt: foundInLeft,
        foundInRightAt: foundInRight,
        remainingObservables: null,
        detail: detail,
        window: window,
      };
      return {
        equal: false,
        matchedObservables: matched,
        divergence: divergence,
        stats: buildStats(leftSegs, rightSegs, lc, rc, internalLeft, internalRight, matched),
      };
    }

    const diffs: ValueDiff[] = [];
    deepDiff(eventView(le), eventView(re), opts.ignore, opts.tolerance, '$', diffs, opts.maxValueDiffs + 1);
    if (diffs.length > 0) {
      const truncated = diffs.length > opts.maxValueDiffs;
      const shown = truncated ? diffs.slice(0, opts.maxValueDiffs) : diffs;
      const first = shown[0];
      const divergence: Divergence = {
        kind: 'value-mismatch',
        observableIndex: matched,
        left: le,
        right: re,
        valueDiffs: shown,
        valueDiffsTruncated: truncated,
        foundInLeftAt: null,
        foundInRightAt: null,
        remainingObservables: null,
        detail:
          'observable #' +
          matched +
          ' "' +
          le.action +
          '" has ' +
          shown.length +
          (truncated ? '+' : '') +
          ' value difference(s); first at ' +
          first.path,
        window: window,
      };
      return {
        equal: false,
        matchedObservables: matched,
        divergence: divergence,
        stats: buildStats(leftSegs, rightSegs, lc, rc, internalLeft, internalRight, matched),
      };
    }

    lc.next();
    rc.next();
    matched++;
    lastMatchedLeft = le;
    lastMatchedRight = re;
  }
}
