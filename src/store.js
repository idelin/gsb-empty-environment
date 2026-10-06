/**
 * BreakpointStore — anchors breakpoints to statements, not line numbers.
 *
 * Core semantics (the explicit statement-identity model):
 * - A breakpoint binds to the innermost statement at its position and keeps
 *   the statement's stable id plus an offset delta relative to the
 *   statement start.
 * - Edits form a chain of versions. Unchanged statements keep their ids
 *   (positions shift); edited statements keep identity via per-region
 *   pairing; moved statements keep identity via content keys, so their
 *   breakpoints follow.
 * - Deleting the statement a breakpoint is bound to makes the breakpoint
 *   UNBOUND. It never slides onto an unrelated line. Its last known
 *   position is kept for display only.
 * - Re-inserting the deleted statement (undo, cut+paste, revert) revives
 *   the statement id from a bounded tombstone buffer, and the breakpoint
 *   rebinds.
 * - Copying a statement creates a NEW statement id; the breakpoint stays
 *   on the original.
 * - Every version snapshots the statement table, so breakpoints can be
 *   mapped to any retained version (cross-version mapping).
 */

import { LineIndex, applyEditsToText, rangesIntersect } from './text.js';
import { parseStatements, innermostStatementAt } from './statements.js';
import { IdentityModel } from './identity.js';

const DEFAULTS = {
  historyLimit: 64,
  tombstoneLimit: 256,
};

export class BreakpointStore {
  constructor(source, options = {}) {
    const opts = { ...DEFAULTS, ...options };
    this.historyLimit = opts.historyLimit;
    this.identity = new IdentityModel({ tombstoneLimit: opts.tombstoneLimit });
    this.source = source;
    this.version = 0;
    this.statements = this.identity.assignInitial(parseStatements(source));
    this.breakpoints = new Map(); // bpId -> { id, statementId, offsetDelta, lastKnown }
    this.nextBreakpointId = 1;
    this.history = new Map(); // version -> { lineStarts, statements: Map<id, {start,end}> }
    this.listeners = new Set();
    this.recordSnapshot();
  }

  // --- subscriptions -------------------------------------------------------

  subscribe(listener) {
    this.listeners.add(listener);
    return () => this.listeners.delete(listener);
  }

  emit(event) {
    for (const fn of this.listeners) fn(event);
  }

  // --- statements ----------------------------------------------------------

  getStatements() {
    return this.statements.map((s) => ({ ...s }));
  }

  getStatementAt(line, column) {
    const li = new LineIndex(this.source);
    const offset = li.positionToOffset(line, column);
    const idx = innermostStatementAt(this.statements, offset);
    return idx === -1 ? null : { ...this.statements[idx] };
  }

  // --- breakpoints ---------------------------------------------------------

  /**
   * Set a breakpoint at a 1-based line/column. Binds to the innermost
   * statement containing that position. Returns the breakpoint, or null
   * when no statement covers the position.
   */
  setBreakpoint(line, column) {
    const li = new LineIndex(this.source);
    const offset = li.positionToOffset(line, column);
    const idx = innermostStatementAt(this.statements, offset);
    if (idx === -1) return null;
    const stmt = this.statements[idx];
    const bp = {
      id: 'bp' + this.nextBreakpointId++,
      statementId: stmt.id,
      offsetDelta: offset - stmt.start,
      lastKnown: { line, column, version: this.version },
    };
    this.breakpoints.set(bp.id, bp);
    this.emit({ type: 'breakpoint-added', breakpoint: this.describeBreakpoint(bp) });
    return this.describeBreakpoint(bp);
  }

  removeBreakpoint(bpId) {
    const bp = this.breakpoints.get(bpId);
    if (!bp) return false;
    this.breakpoints.delete(bpId);
    this.emit({ type: 'breakpoint-removed', breakpointId: bpId });
    return true;
  }

  /** Breakpoints resolved against the current version. */
  getBreakpoints() {
    return [...this.breakpoints.values()].map((bp) => this.describeBreakpoint(bp));
  }

  describeBreakpoint(bp) {
    const stmt = this.statements.find((s) => s.id === bp.statementId);
    if (stmt) {
      const li = new LineIndex(this.source);
      const pos = li.offsetToPosition(stmt.start + bp.offsetDelta);
      return {
        id: bp.id,
        status: 'bound',
        statementId: bp.statementId,
        line: pos.line,
        column: pos.column,
        lastKnown: { ...bp.lastKnown },
      };
    }
    return {
      id: bp.id,
      status: 'unbound',
      statementId: bp.statementId,
      line: null,
      column: null,
      lastKnown: { ...bp.lastKnown },
    };
  }

  // --- edits ----------------------------------------------------------------

  /**
   * Apply a single edit: { start, end, text } with 0-based half-open offsets
   * in the current source. Advances the version by one.
   */
  applyEdit(edit) {
    return this.applyEdits([edit]);
  }

  /**
   * Apply a batch of edits expressed in the coordinate system of the current
   * source (a single user operation). The batch is one version step, so a
   * statement moved by a coordinated delete+insert keeps its identity.
   */
  applyEdits(edits) {
    if (edits.length === 0) return this.version;
    const prevStatements = this.statements;
    const nextSource = applyEditsToText(this.source, edits);
    const nextStatements = parseStatements(nextSource);
    const { matched, deleted } = this.identity.matchStatements(prevStatements, nextStatements, edits);

    this.source = nextSource;
    this.statements = matched;
    this.version++;
    this.recordSnapshot();

    // Refresh lastKnown for breakpoints that are still bound.
    const li = new LineIndex(this.source);
    const rebound = [];
    const unbound = [];
    for (const bp of this.breakpoints.values()) {
      const stmt = this.statements.find((s) => s.id === bp.statementId);
      if (stmt) {
        const pos = li.offsetToPosition(stmt.start + bp.offsetDelta);
        bp.lastKnown = { line: pos.line, column: pos.column, version: this.version };
        rebound.push(bp.id);
      } else {
        unbound.push(bp.id);
      }
    }
    this.emit({
      type: 'version',
      version: this.version,
      edits,
      deletedStatementIds: deleted.map((d) => d.id),
      reboundBreakpointIds: rebound,
      unboundBreakpointIds: unbound,
    });
    return this.version;
  }

  /**
   * Replace the whole source (e.g. loading a different file revision).
   * Identity is recovered purely through content keys and tombstones.
   */
  setSource(nextSource) {
    return this.applyEdits([{ start: 0, end: this.source.length, text: nextSource }]);
  }

  // --- cross-version mapping -------------------------------------------------

  recordSnapshot() {
    const li = new LineIndex(this.source);
    const table = new Map();
    for (const s of this.statements) {
      table.set(s.id, { start: s.start, end: s.end });
    }
    this.history.set(this.version, { lineStarts: li.starts, statements: table });
    // Evict oldest versions beyond the limit (never the current one).
    while (this.history.size > this.historyLimit) {
      const oldest = Math.min(...this.history.keys());
      if (oldest === this.version) break;
      this.history.delete(oldest);
    }
  }

  /**
   * Map a breakpoint to a retained version. Returns
   * { status: 'bound', line, column } when the bound statement exists in
   * that version, { status: 'unbound' } when it does not, and null when the
   * version or breakpoint is unknown.
   */
  mapBreakpointToVersion(bpId, version) {
    const bp = this.breakpoints.get(bpId);
    const snap = this.history.get(version);
    if (!bp || !snap) return null;
    const span = snap.statements.get(bp.statementId);
    if (!span) return { status: 'unbound', line: null, column: null };
    const li = new LineIndex('');
    li.starts = snap.lineStarts;
    const pos = li.offsetToPosition(span.start + bp.offsetDelta);
    return { status: 'bound', line: pos.line, column: pos.column };
  }

  getVersion() {
    return this.version;
  }

  getSource() {
    return this.source;
  }
}
