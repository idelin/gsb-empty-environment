/**
 * Finite state exploration.
 *
 * Enumerates every event sequence up to a fixed depth over a small,
 * deterministic event alphabet and checks the public invariants at every
 * reachable state and across every transition:
 *
 *   I1  commit happens at most once per generation (no double insert).
 *   I2  a cancel step never modifies the formal text (so it can never
 *       undo remote content).
 *   I3  a conflicting remote edit still applies in full even though it
 *       ends the composition.
 *   I4  no formal undo-history snapshot ever equals an intermediate
 *       (uncommitted) composition projection.
 *   I5  the projection is always exactly `formal` with the composition
 *       range replaced by the composed text.
 *   I6  neither formal text nor projection ever contains lone surrogates.
 *   I7  a commit transition grows the undo history by at most one unit.
 *   I8  stale / duplicate callbacks are ignored and change nothing.
 *
 * Each frontier state is rebuilt by replaying its event path from the root,
 * so explored states are fully independent (paths are short: <= maxDepth).
 */

import { Editor, type CompositionHandle } from './editor.ts';
import { applyEdit, spliceRange, type Edit } from './rules.ts';
import { hasLoneSurrogates } from './utf16.ts';

const UPDATE_A = 'あ';
const UPDATE_B = '好👍'; // CJK + surrogate pair
const REMOTE_TEXT = 'R';
const REMOTE_TEXT_2 = 'Q';
const ROOT_TEXT = 'ab好c\r\nde👍f'; // CJK, CRLF and a surrogate pair

interface Coverage {
  conflictCancels: number;
  transformed: number;
  staleIgnored: number;
  duplicateIgnored: number;
  undoDuringComposition: number;
  commits: number;
  cancels: number;
}

interface Ctx {
  violations: string[];
  /** Projections of still-active sessions, keyed by generation. */
  sessionProjections: Map<number, Set<string>>;
  /** Projections of sessions that ended WITHOUT committing. */
  forbiddenProjections: Set<string>;
  /** Handles of settled sessions, for stale/duplicate callbacks. */
  deadHandles: CompositionHandle[];
  coverage: Coverage;
}

interface Step {
  editor: Editor;
  ctx: Ctx;
  path: string[];
}

export interface ExploreResult {
  states: number;
  transitions: number;
  maxDepth: number;
  violations: string[];
  coverage: Coverage;
}

function emptyCoverage(): Coverage {
  return {
    conflictCancels: 0,
    transformed: 0,
    staleIgnored: 0,
    duplicateIgnored: 0,
    undoDuringComposition: 0,
    commits: 0,
    cancels: 0,
  };
}

function makeCtx(violations: string[]): Ctx {
  return {
    violations,
    sessionProjections: new Map(),
    forbiddenProjections: new Set(),
    deadHandles: [],
    coverage: emptyCoverage(),
  };
}

function stateKey(editor: Editor): string {
  return JSON.stringify({
    formal: editor.getFormalText(),
    sel: editor.getSelection(),
    comp: editor.currentComposition(),
    undo: editor.undoDepth,
    redo: editor.redoDepth,
    snaps: editor.historySnapshots(),
  });
}

function checkState(step: Step, where: string): void {
  const { editor, ctx } = step;
  const formal = editor.getFormalText();
  const projection = editor.getProjection();
  const comp = editor.currentComposition();
  const at = `${where} :: ${step.path.join('>') || '(root)'}`;

  if (hasLoneSurrogates(formal) || hasLoneSurrogates(projection)) {
    ctx.violations.push(`I6 lone surrogate @ ${at}`);
  }
  if (comp) {
    if (projection !== spliceRange(formal, comp.range, comp.text)) {
      ctx.violations.push(`I5 projection mismatch @ ${at}`);
    }
    if (
      comp.range.start < 0 ||
      comp.range.end > formal.length ||
      comp.range.start > comp.range.end
    ) {
      ctx.violations.push(`I5 range out of bounds @ ${at}`);
    }
  }
  for (const [gen, count] of editor.stats.commitsByGeneration) {
    if (count > 1) ctx.violations.push(`I1 generation ${gen} committed ${count}x @ ${at}`);
  }
  for (const snap of editor.historySnapshots()) {
    if (ctx.forbiddenProjections.has(snap)) {
      ctx.violations.push(`I4 intermediate state in history @ ${at}`);
    }
  }
  if (ctx.forbiddenProjections.has(formal)) {
    ctx.violations.push(`I4 intermediate state became formal @ ${at}`);
  }
}

function trackProjection(step: Step): void {
  const comp = step.editor.currentComposition();
  if (!comp) return;
  let set = step.ctx.sessionProjections.get(comp.id);
  if (!set) {
    set = new Set();
    step.ctx.sessionProjections.set(comp.id, set);
  }
  set.add(step.editor.getProjection());
}

/** Move a settled session's projections into the forbidden set. */
function settleProjections(step: Step, generation: number, committed: boolean): void {
  const set = step.ctx.sessionProjections.get(generation);
  step.ctx.sessionProjections.delete(generation);
  if (!set || committed) return;
  for (const projection of set) step.ctx.forbiddenProjections.add(projection);
}

type Event = (step: Step) => void;

function remoteEdit(step: Step, edit: Edit, label: string): void {
  const { editor, ctx } = step;
  const gen = editor.currentComposition()?.id ?? null;
  const formalBefore = editor.getFormalText();
  const outcome = editor.applyRemoteEdit(edit);
  if (editor.getFormalText() !== applyEdit(formalBefore, edit)) {
    ctx.violations.push(`I3 remote edit not fully applied @ ${label} :: ${step.path.join('>')}`);
  }
  if (outcome === 'ended-composition') {
    ctx.coverage.conflictCancels += 1;
    if (gen !== null) {
      settleProjections(step, gen, editor.stats.commitsByGeneration.has(gen));
    }
  } else if (outcome === 'transformed-composition') {
    ctx.coverage.transformed += 1;
  }
}

const EVENTS: Record<string, Event> = {
  start(step) {
    step.editor.compositionStart();
  },
  'start-over-selection'(step) {
    if (step.editor.getFormalText().length < 3) return;
    step.editor.setSelection(1, 3);
    step.editor.compositionStart();
  },
  'update-a'(step) {
    const comp = step.editor.currentComposition();
    if (!comp) return;
    step.editor.updateComposition(UPDATE_A, comp.id);
    trackProjection(step);
  },
  'update-b'(step) {
    const comp = step.editor.currentComposition();
    if (!comp) return;
    step.editor.updateComposition(comp.text + UPDATE_B, comp.id);
    trackProjection(step);
  },
  commit(step) {
    const comp = step.editor.currentComposition();
    if (!comp) return;
    const undoBefore = step.editor.undoDepth;
    step.editor.commitComposition(comp.id);
    const growth = step.editor.undoDepth - undoBefore;
    if (growth > 1) {
      step.ctx.violations.push(`I7 commit grew history by ${growth} :: ${step.path.join('>')}`);
    }
    settleProjections(step, comp.id, true);
    step.ctx.coverage.commits += 1;
  },
  cancel(step) {
    const comp = step.editor.currentComposition();
    if (!comp) return;
    const formalBefore = step.editor.getFormalText();
    step.editor.cancelComposition(comp.id, 'explicit');
    if (step.editor.getFormalText() !== formalBefore) {
      step.ctx.violations.push(`I2 cancel modified formal text :: ${step.path.join('>')}`);
    }
    settleProjections(step, comp.id, false);
    step.ctx.coverage.cancels += 1;
  },
  'remote-before'(step) {
    remoteEdit(step, { start: 0, end: 0, text: REMOTE_TEXT }, 'remote-before');
  },
  'remote-after'(step) {
    const len = step.editor.getFormalText().length;
    remoteEdit(step, { start: len, end: len, text: REMOTE_TEXT_2 }, 'remote-after');
  },
  'remote-inside'(step) {
    const comp = step.editor.currentComposition();
    if (!comp || comp.range.end - comp.range.start < 2) return;
    remoteEdit(
      step,
      { start: comp.range.start + 1, end: comp.range.start + 1, text: REMOTE_TEXT },
      'remote-inside',
    );
  },
  'remote-delete-head'(step) {
    const len = step.editor.getFormalText().length;
    if (len === 0) return;
    remoteEdit(step, { start: 0, end: Math.min(2, len), text: '' }, 'remote-delete-head');
  },
  undo(step) {
    const gen = step.editor.currentComposition()?.id ?? null;
    if (gen !== null) step.ctx.coverage.undoDuringComposition += 1;
    step.editor.undo();
    if (gen !== null) settleProjections(step, gen, false);
  },
  redo(step) {
    const gen = step.editor.currentComposition()?.id ?? null;
    step.editor.redo();
    if (gen !== null) settleProjections(step, gen, false);
  },
  blur(step) {
    const comp = step.editor.currentComposition();
    if (!comp) return;
    step.editor.blur();
    settleProjections(step, comp.id, step.editor.stats.commitsByGeneration.has(comp.id));
  },
  'stale-update'(step) {
    const handle = step.ctx.deadHandles[step.ctx.deadHandles.length - 1];
    if (!handle) return;
    const formalBefore = step.editor.getFormalText();
    const projectionBefore = step.editor.getProjection();
    if (handle.update('💥late') === 'applied') {
      step.ctx.violations.push(`I8 stale update applied :: ${step.path.join('>')}`);
    }
    if (
      step.editor.getFormalText() !== formalBefore ||
      step.editor.getProjection() !== projectionBefore
    ) {
      step.ctx.violations.push(`I8 stale update mutated state :: ${step.path.join('>')}`);
    }
    step.ctx.coverage.staleIgnored += 1;
  },
  'duplicate-commit'(step) {
    const handle = step.ctx.deadHandles[step.ctx.deadHandles.length - 1];
    if (!handle) return;
    const formalBefore = step.editor.getFormalText();
    if (handle.commit() === 'applied') {
      step.ctx.violations.push(`I8 duplicate commit applied :: ${step.path.join('>')}`);
    }
    if (step.editor.getFormalText() !== formalBefore) {
      step.ctx.violations.push(`I1 duplicate commit mutated formal :: ${step.path.join('>')}`);
    }
    step.ctx.coverage.duplicateIgnored += 1;
  },
};

const EVENT_NAMES = Object.keys(EVENTS);

/** A handle bound to a settled generation, used for stale callbacks. */
function makeDeadHandle(editor: Editor, id: number): CompositionHandle {
  return {
    id,
    update: (text: string) => editor.updateComposition(text, id),
    commit: () => editor.commitComposition(id),
    cancel: () => editor.cancelComposition(id, 'explicit'),
  };
}

/** Rebuild an independent editor+ctx by replaying an event path. */
function replay(path: string[], violations: string[]): Step {
  const step: Step = {
    editor: new Editor({ text: ROOT_TEXT }),
    ctx: makeCtx(violations),
    path: [],
  };
  for (const name of path) {
    const before = step.editor.currentComposition();
    EVENTS[name](step);
    const after = step.editor.currentComposition();
    if (before && (!after || after.id !== before.id)) {
      step.ctx.deadHandles.push(makeDeadHandle(step.editor, before.id));
    }
    step.path.push(name);
  }
  return step;
}

export function explore(maxDepth: number): ExploreResult {
  const violations: string[] = [];
  const root: Step = {
    editor: new Editor({ text: ROOT_TEXT }),
    ctx: makeCtx(violations),
    path: [],
  };
  checkState(root, 'root');

  const seen = new Set<string>([stateKey(root.editor)]);
  let frontier: Step[] = [root];
  let states = 1;
  let transitions = 0;
  const coverage = emptyCoverage();

  for (let depth = 0; depth < maxDepth; depth += 1) {
    const next: Step[] = [];
    for (const step of frontier) {
      for (const name of EVENT_NAMES) {
        const branched = replay(step.path.concat(name), violations);
        transitions += 1;
        checkState(branched, name);
        const c = branched.ctx.coverage;
        coverage.conflictCancels += c.conflictCancels;
        coverage.transformed += c.transformed;
        coverage.staleIgnored += c.staleIgnored;
        coverage.duplicateIgnored += c.duplicateIgnored;
        coverage.undoDuringComposition += c.undoDuringComposition;
        coverage.commits += c.commits;
        coverage.cancels += c.cancels;
        const key = stateKey(branched.editor);
        if (seen.has(key)) continue;
        seen.add(key);
        states += 1;
        next.push(branched);
      }
    }
    frontier = next;
  }

  return { states, transitions, maxDepth, violations, coverage };
}
