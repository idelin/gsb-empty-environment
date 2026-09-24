import type { History, MappingStep } from './history.ts';
import { invertMapping, mapRange } from './mapping.ts';
import { assertUtf16Boundary, splitsSurrogatePair } from './utf16.ts';

export const CONTEXT_RADIUS = 24;

export interface ContextSummary {
  before: string;
  text: string;
  after: string;
}

export interface Diagnostic {
  id: string;
  version: number;
  start: number;
  end: number;
  context: ContextSummary;
  message: string;
}

export interface Candidate {
  start: number;
  end: number;
  match: 'full-context' | 'text-only';
  excluded?: string;
}

export interface Evidence {
  steps: string[];
}

export type RelocationResult =
  | { kind: 'definite'; via: 'identity' | 'context'; start: number; end: number; evidence: Evidence }
  | { kind: 'ambiguous'; candidates: Candidate[]; evidence: Evidence }
  | { kind: 'invalid'; reason: string; excluded: Candidate[]; evidence: Evidence };

let diagnosticCounter = 0;

export function captureContext(
  text: string,
  start: number,
  end: number,
  radius = CONTEXT_RADIUS,
): ContextSummary {
  let beforeStart = Math.max(0, start - radius);
  while (beforeStart < start && splitsSurrogatePair(text, beforeStart)) beforeStart++;
  let afterEnd = Math.min(text.length, end + radius);
  while (afterEnd > end && splitsSurrogatePair(text, afterEnd)) afterEnd--;
  return {
    before: text.slice(beforeStart, start),
    text: text.slice(start, end),
    after: text.slice(end, afterEnd),
  };
}

export function createDiagnostic(
  history: History,
  start: number,
  end: number,
  message = '',
  id?: string,
): Diagnostic {
  const text = history.text();
  assertUtf16Boundary(text, start, 'diagnostic start');
  assertUtf16Boundary(text, end, 'diagnostic end');
  if (start > end) throw new RangeError('diagnostic start > diagnostic end');
  return {
    id: id ?? 'd' + ++diagnosticCounter,
    version: history.current,
    start,
    end,
    context: captureContext(text, start, end),
    message,
  };
}

export function findCandidates(doc: string, context: ContextSummary): Candidate[] {
  const candidates: Candidate[] = [];
  if (context.text.length === 0) return candidates;
  let index = doc.indexOf(context.text);
  while (index !== -1) {
    const end = index + context.text.length;
    const before = doc.slice(Math.max(0, index - context.before.length), index);
    const after = doc.slice(end, end + context.after.length);
    const full = before === context.before && after === context.after;
    candidates.push({ start: index, end, match: full ? 'full-context' : 'text-only' });
    index = doc.indexOf(context.text, index + 1);
  }
  return candidates;
}

function describeStep(step: MappingStep): string {
  const prefix = step.direction === 'undo' ? 'undo of ' : '';
  const edit = step.version.edit;
  if (!edit) return prefix + 'checkpoint v' + step.version.id;
  switch (edit.kind) {
    case 'insert':
      return prefix + 'insert#' + edit.id + ' @' + edit.offset + ' ' + JSON.stringify(edit.text);
    case 'delete':
      return prefix + 'delete#' + edit.id + ' [' + edit.start + ', ' + edit.end + ')';
    case 'move':
      return (
        prefix +
        'move#' + edit.id + ' [' + edit.start + ', ' + edit.end + ') -> ' + edit.to +
        ' (stableId=' + edit.stableId + ')'
      );
  }
}

export function relocate(history: History, diagnostic: Diagnostic): RelocationResult {
  const steps: string[] = [];
  const path = history.mappingBetween(diagnostic.version, history.current);
  if (path) {
    steps.push(
      'identity path v' + diagnostic.version + ' -> v' + history.current +
      ' (' + path.steps.length + ' edit(s), composed into one mapping)',
    );
    let cursor = { start: diagnostic.start, end: diagnostic.end };
    let lost = false;
    for (const step of path.steps) {
      const stepMapping =
        step.direction === 'forward' ? step.version.mapping! : invertMapping(step.version.mapping!);
      const mapped = mapRange(stepMapping, cursor.start, cursor.end);
      steps.push(
        describeStep(step) + ': [' + cursor.start + ', ' + cursor.end + ') -> ' +
        (mapped.status === 'deleted' ? 'DELETED' : '[' + mapped.start + ', ' + mapped.end + ')'),
      );
      if (mapped.status === 'deleted') {
        lost = true;
        break;
      }
      cursor = { start: mapped.start, end: mapped.end };
    }
    const composed = mapRange(path.mapping, diagnostic.start, diagnostic.end);
    if (composed.status !== 'deleted' && !lost) {
      if (composed.deletedUnits > 0) {
        steps.push('range partially deleted (' + composed.deletedUnits + ' UTF-16 unit(s) lost)');
      }
      return {
        kind: 'definite',
        via: 'identity',
        start: composed.start,
        end: composed.end,
        evidence: { steps },
      };
    }
    steps.push('identity lost: target range fully deleted');
    const excluded = findCandidates(history.text(), diagnostic.context).map((c) => ({
      ...c,
      excluded:
        'identity lost at delete; identical text elsewhere cannot revive diagnostic ' + diagnostic.id,
    }));
    for (const c of excluded) {
      steps.push(
        'candidate [' + c.start + ', ' + c.end + ') generated by ' + c.match +
        ' match, excluded: no stable identity (deleted, not moved)',
      );
    }
    return {
      kind: 'invalid',
      reason: 'target range deleted',
      excluded,
      evidence: { steps },
    };
  }
  steps.push(
    'no identity path: version v' + diagnostic.version +
    ' reclaimed or on an abandoned branch; falling back to context summary',
  );
  const candidates = findCandidates(history.text(), diagnostic.context);
  steps.push(
    'context search generated ' + candidates.length + ' candidate(s): ' +
    (candidates.length === 0
      ? 'none'
      : candidates.map((c) => '[' + c.start + ', ' + c.end + ') ' + c.match).join(', ')),
  );
  const full = candidates.filter((c) => c.match === 'full-context');
  const best = full.length > 0 ? full : candidates;
  if (best.length === 0) {
    return {
      kind: 'invalid',
      reason: 'no matching context in current text',
      excluded: [],
      evidence: { steps },
    };
  }
  if (best.length === 1) {
    steps.push('unique ' + best[0]!.match + ' candidate accepted');
    return {
      kind: 'definite',
      via: 'context',
      start: best[0]!.start,
      end: best[0]!.end,
      evidence: { steps },
    };
  }
  steps.push(
    best.length + ' equivalent candidates remain; refusing to pick the first one arbitrarily',
  );
  return { kind: 'ambiguous', candidates: best, evidence: { steps } };
}
