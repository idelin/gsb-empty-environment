import type { JsonValue, Query, QueryResult } from "./ast.ts";
import type { BudgetLimits } from "./budget.ts";
import { Budget } from "./budget.ts";
import { evaluate, jsonEqual } from "./interpreter.ts";
import { parseQuery } from "./parser.ts";
import { encodePath, type PathSeg } from "./path.ts";
import type { VersionedDocument } from "./document.ts";

export interface ResultDiff {
  version: number;
  added: QueryResult[];
  removed: QueryResult[];
  changed: Array<{ path: PathSeg[]; oldValue: JsonValue; newValue: JsonValue }>;
}

/**
 * A stable cursor: pins a path to the document version at which it was
 * created. Resolving it reads that version's snapshot, so edits made
 * afterwards (e.g. deleting an array element that shifts later indices)
 * do not change what an old cursor points to.
 */
export interface Cursor {
  readonly version: number;
  readonly path: PathSeg[];
}

export type CursorResolution = { ok: true; value: JsonValue } | { ok: false };

export class IncrementalQuery {
  private readonly doc: VersionedDocument;
  private readonly query: Query;
  private readonly limits: BudgetLimits;
  private current = new Map<string, QueryResult>();
  private syncedVersion = -1;

  constructor(doc: VersionedDocument, source: string | Query, opts: { budget?: BudgetLimits } = {}) {
    this.doc = doc;
    this.query = typeof source === "string" ? parseQuery(source) : source;
    this.limits = opts.budget ?? {};
  }

  /** Re-evaluates if the document changed and returns the result diff. */
  sync(): ResultDiff {
    const version = this.doc.version;
    if (version === this.syncedVersion) {
      return { version, added: [], removed: [], changed: [] };
    }
    const budget = new Budget(this.limits);
    const next = evaluate(this.doc.get(), this.query, budget);
    const nextMap = new Map<string, QueryResult>();
    for (const r of next) nextMap.set(encodePath(r.path), r);

    const diff: ResultDiff = { version, added: [], removed: [], changed: [] };
    for (const [key, r] of nextMap) {
      const prev = this.current.get(key);
      if (prev === undefined) {
        diff.added.push(r);
      } else if (!jsonEqual(prev.value, r.value)) {
        diff.changed.push({ path: r.path, oldValue: prev.value, newValue: r.value });
      }
    }
    for (const [key, r] of this.current) {
      if (!nextMap.has(key)) diff.removed.push(r);
    }
    this.current = nextMap;
    this.syncedVersion = version;
    return diff;
  }

  results(): QueryResult[] {
    return [...this.current.values()];
  }

  cursor(path: PathSeg[]): Cursor {
    return { version: this.doc.version, path: [...path] };
  }

  resolve(cursor: Cursor): CursorResolution {
    let snap: JsonValue;
    try {
      snap = this.doc.get(cursor.version);
    } catch {
      return { ok: false };
    }
    let current: JsonValue = snap;
    for (const seg of cursor.path) {
      if (Array.isArray(current)) {
        if (typeof seg !== "number" || seg < 0 || seg >= current.length) return { ok: false };
        current = current[seg];
      } else if (current !== null && typeof current === "object") {
        if (typeof seg !== "string" || !Object.hasOwn(current, seg)) return { ok: false };
        current = current[seg];
      } else {
        return { ok: false };
      }
    }
    return { ok: true, value: current };
  }
}
