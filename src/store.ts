/**
 * BreakpointStore: anchors breakpoints to statements of a TypeScript source
 * and carries their (line, character) anchors through a chain of edits.
 *
 * Semantics of the explicit statement identity model:
 *
 *  - Bound: the breakpoint's statement identity (sid) exists in the current
 *    version. The anchor tracks the statement's position, so it follows the
 *    statement when lines are inserted/removed around it and when the
 *    statement itself is moved (its content key is position-independent).
 *  - Unbound: the statement was deleted (its identity retired). The
 *    breakpoint keeps its last known position for display but is NOT moved
 *    onto whatever unrelated statement now occupies that line.
 *  - Rebind: an unbound breakpoint rebinds when a statement with its exact
 *    content key reappears, provided the key was absent in the previous
 *    version (delete-then-restore). If an identical statement existed all
 *    along, deleting one copy does not make the breakpoint jump to the
 *    surviving copy.
 *  - Copy: duplicating a statement gives the copy a fresh identity; the
 *    breakpoint stays on the original occurrence.
 *
 * Positions are 1-based (line, character), matching tsc conventions.
 */

import { extractStatements, computeLineStarts, lineCharToOffset } from './statements.ts';
import type { Statement } from './statements.ts';
import { reconcile } from './reconcile.ts';

export type BreakpointStatus = 'bound' | 'unbound';

export interface Breakpoint {
  id: number;
  status: BreakpointStatus;
  /** 1-based position in `version`; for unbound breakpoints the last known position. */
  line: number;
  character: number;
  /** Identity of the bound statement, or null while unbound. */
  statementId: number | null;
  /** Content key of the statement the breakpoint belongs to ('' if none). */
  statementKey: string;
  /** Version this position is valid for. */
  version: number;
}

export interface BreakpointSnapshot {
  status: BreakpointStatus;
  line: number;
  character: number;
  statementId: number | null;
}

interface InternalStatement extends Statement {
  sid: number;
}

interface InternalBreakpoint {
  status: BreakpointStatus;
  key: string;
  sid: number | null;
  line: number;
  character: number;
}

export class BreakpointStore {
  private source: string;
  private lineStarts: number[] = [];
  private statements: InternalStatement[] = [];
  private breakpoints = new Map<number, InternalBreakpoint>();
  private history = new Map<number, Map<number, BreakpointSnapshot>>();
  private nextSid = 1;
  private nextBid = 1;

  /** Current version; starts at 1 and increments on every applyEdit. */
  readonly version: number = 0;

  constructor(source: string) {
    this.source = source;
    this.reparse(source, true);
  }

  getVersion(): number {
    return this.version;
  }

  getStatements(): ReadonlyArray<Statement & { sid: number }> {
    return this.statements;
  }

  /** Place a breakpoint at a 1-based (line, character) position. */
  addBreakpoint(line: number, character: number = 1): Breakpoint {
    const offset = lineCharToOffset(this.lineStarts, this.source.length, line, character);

    // Innermost statement containing the position.
    let best: InternalStatement | null = null;
    for (const s of this.statements) {
      if (s.start <= offset && offset <= s.end) {
        if (!best || s.end - s.start < best.end - best.start) best = s;
      }
    }
    // Fallback: nearest statement starting at or after the position.
    if (!best) {
      for (const s of this.statements) {
        if (s.start >= offset && (!best || s.start < best.start)) best = s;
      }
    }

    const id = this.nextBid++;
    const record: InternalBreakpoint = best
      ? { status: 'bound', key: best.key, sid: best.sid, line: best.startLine, character: best.startChar }
      : { status: 'unbound', key: '', sid: null, line, character };
    this.breakpoints.set(id, record);
    this.snapshot();
    return this.toPublic(id, record);
  }

  /** Advance the edit chain: replace the source, producing a new version. */
  applyEdit(newSource: string): void {
    this.reparse(newSource, false);
  }

  getBreakpoints(): Breakpoint[] {
    return [...this.breakpoints.entries()].map(([id, bp]) => this.toPublic(id, bp));
  }

  getBreakpoint(id: number): Breakpoint | null {
    const bp = this.breakpoints.get(id);
    return bp ? this.toPublic(id, bp) : null;
  }

  /** Position/status of a breakpoint as of a past version (cross-version mapping). */
  getBreakpointAtVersion(id: number, version: number): BreakpointSnapshot | null {
    return this.history.get(version)?.get(id) ?? null;
  }

  private reparse(source: string, first: boolean): void {
    const extracted = extractStatements(source);

    if (first) {
      this.statements = extracted.map((s) => ({ ...s, sid: this.nextSid++ }));
    } else {
      const oldKeyed = this.statements.map((s) => ({ sid: s.sid, key: s.key }));
      const oldKeys = new Set(oldKeyed.map((k) => k.key));
      const { assignments } = reconcile(oldKeyed, extracted.map((s) => s.key));

      this.statements = extracted.map((s, idx) => ({
        ...s,
        sid: assignments[idx] ?? this.nextSid++,
      }));

      const bySid = new Map<number, InternalStatement>();
      const firstByKey = new Map<string, InternalStatement>();
      for (const s of this.statements) {
        bySid.set(s.sid, s);
        if (!firstByKey.has(s.key)) firstByKey.set(s.key, s);
      }

      for (const bp of this.breakpoints.values()) {
        if (bp.status === 'bound') {
          const st = bp.sid !== null ? bySid.get(bp.sid) : undefined;
          if (st) {
            bp.line = st.startLine;
            bp.character = st.startChar;
          } else {
            // Statement deleted: unbind, keep last known position.
            bp.status = 'unbound';
            bp.sid = null;
          }
        } else if (bp.key !== '' && !oldKeys.has(bp.key)) {
          // Delete-then-restore: the key was absent, now it is back.
          const st = firstByKey.get(bp.key);
          if (st) {
            bp.status = 'bound';
            bp.sid = st.sid;
            bp.line = st.startLine;
            bp.character = st.startChar;
          }
        }
      }
    }

    this.source = source;
    this.lineStarts = computeLineStarts(source);
    (this as { version: number }).version++;
    this.snapshot();
  }

  private snapshot(): void {
    const map = new Map<number, BreakpointSnapshot>();
    for (const [id, bp] of this.breakpoints) {
      map.set(id, { status: bp.status, line: bp.line, character: bp.character, statementId: bp.sid });
    }
    this.history.set(this.version, map);
  }

  private toPublic(id: number, bp: InternalBreakpoint): Breakpoint {
    return {
      id,
      status: bp.status,
      line: bp.line,
      character: bp.character,
      statementId: bp.sid,
      statementKey: bp.key,
      version: this.version,
    };
  }
}
