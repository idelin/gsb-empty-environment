import type { JsonValue, Query, QueryResult, Segment, SimpleSegment } from "./ast.ts";
import { needsStaging } from "./ast.ts";
import { Budget, type BudgetLimits } from "./budget.ts";
import { CancelledError } from "./errors.ts";
import { buildValue, type TreeEvent } from "./events.ts";
import { evaluateSegments, exprPasses } from "./interpreter.ts";
import { encodePath, escapeSeg, isPrefix, pathEquals, type PathSeg } from "./path.ts";
import { FileSpillStore } from "./spill.ts";

export interface StreamOptions {
  /** query budget; a fresh Budget is created from these limits */
  budget?: BudgetLimits;
  /** max events held in memory per staged subtree before spilling to disk */
  bufferLimit?: number;
  /** directory for spill files (defaults to os.tmpdir()) */
  spillDir?: string;
  /** abort to cancel; all temporary spill data is removed */
  signal?: AbortSignal;
}

const DEFAULT_BUFFER_LIMIT = 1024;

type ContainerKind = "array" | "object";

/** Records the events of one subtree; spills to disk past the memory limit. */
class EventRecorder {
  private mem: TreeEvent[] = [];
  private spill: import("./spill.ts").SpillWriter | null = null;
  readonly key: string;
  readonly base: PathSeg[];
  private readonly limit: number;
  private readonly store: FileSpillStore;
  private readonly onClose: (value: JsonValue) => QueryResult[] | Promise<QueryResult[]>;

  constructor(
    key: string,
    base: PathSeg[],
    limit: number,
    store: FileSpillStore,
    onClose: (value: JsonValue) => QueryResult[] | Promise<QueryResult[]>,
  ) {
    this.key = key;
    this.base = [...base];
    this.limit = limit;
    this.store = store;
    this.onClose = onClose;
  }

  isDone(ev: TreeEvent): boolean {
    return ev.type === "close" && pathEquals(this.base, ev.path);
  }

  async push(ev: TreeEvent): Promise<void> {
    if (this.spill !== null) {
      await this.spill.write(ev);
      return;
    }
    this.mem.push(ev);
    if (this.mem.length > this.limit) {
      this.spill = await this.store.createWriter();
      for (const e of this.mem) await this.spill.write(e);
      this.mem = [];
    }
  }

  async finalize(): Promise<QueryResult[]> {
    let events: TreeEvent[];
    if (this.spill !== null) {
      events = await this.spill.readAll();
    } else {
      events = this.mem;
    }
    await this.dispose();
    const value = buildValue(events);
    return await this.onClose(value);
  }

  async dispose(): Promise<void> {
    this.mem = [];
    if (this.spill !== null) {
      await this.spill.dispose();
      this.spill = null;
    }
  }
}

interface FilterInstrument {
  path: PathSeg[];
  /** segments starting with the filter; remainder evaluated per passing child */
  rest: Segment[];
}

interface Matcher {
  seg: number;
}

function segKey(seg: PathSeg): string {
  return typeof seg === "number" ? String(seg) : escapeSeg(seg);
}

interface RecursiveMatcher {
  seg: number;
  base: PathSeg[];
}

/** Matches a child arriving from the stream against a non-staged segment. */
function matchStreaming(
  seg: SimpleSegment,
  key: PathSeg,
  parentKind: ContainerKind | undefined,
): boolean {
  switch (seg.kind) {
    case "field":
      return parentKind === "object" && key === seg.name;
    case "index":
      return parentKind === "array" && key === seg.index;
    case "wildcard":
      return true;
    case "slice": {
      if (parentKind !== "array" || typeof key !== "number") return false;
      const start = seg.start ?? 0;
      const step = seg.step ?? 1;
      const end = seg.end ?? Number.POSITIVE_INFINITY;
      return key >= start && key < end && (key - start) % step === 0;
    }
    case "filter":
      return false;
  }
}

function segmentKey(seg: Segment): string {
  switch (seg.kind) {
    case "field": return `f:${seg.name}`;
    case "index": return `i:${seg.index}`;
    case "slice": return `s:${seg.start}:${seg.end}:${seg.step}`;
    case "wildcard": return "w";
    case "filter": return `q:${JSON.stringify(seg.expr)}`;
    case "recursive": return `r:${segmentKey(seg.inner)}`;
  }
}

class StreamEngine {
  private readonly query: Query;
  private readonly budget: Budget;
  private readonly bufferLimit: number;
  private readonly store: FileSpillStore;
  private readonly signal?: AbortSignal;

  private started = false;
  /** keyStack[d] / kindStack[d]: encoded path and kind of the depth-d container. */
  private readonly keyStack: string[] = [];
  private readonly kindStack: ContainerKind[] = [];
  private readonly matchers = new Map<string, Matcher[]>();
  private recMatchers: RecursiveMatcher[] = [];
  private readonly filters = new Map<string, FilterInstrument>();
  private readonly instrumentKeys = new Map<string, Set<string>>();
  private readonly recorders = new Map<string, EventRecorder>();
  private readonly emitted = new Set<string>();

  constructor(query: Query, opts: StreamOptions) {
    this.query = query;
    this.budget = new Budget(opts.budget ?? {});
    this.bufferLimit = opts.bufferLimit ?? DEFAULT_BUFFER_LIMIT;
    this.store = new FileSpillStore(opts.spillDir);
    this.signal = opts.signal;
  }

  get spillStore(): FileSpillStore {
    return this.store;
  }

  async handle(ev: TreeEvent): Promise<QueryResult[]> {
    if (this.signal?.aborted) throw new CancelledError();
    this.budget.charge("expand");
    const out: QueryResult[] = [];
    const depth = ev.path.length;
    if (ev.type === "open") {
      this.keyStack[depth] = depth === 0
        ? ""
        : this.keyStack[depth - 1] + "/" + segKey(ev.path[depth - 1]);
      this.kindStack[depth] = ev.kind;
    }
    if (!this.started) {
      this.started = true;
      await this.advance(ev, 0, out);
    }

    if (depth > 0 && ev.type !== "close") {
      const key = ev.path[depth - 1];
      const parentKey = this.keyStack[depth - 1];
      const parentKind = this.kindStack[depth - 1];

      const filter = this.filters.get(parentKey);
      if (filter !== undefined) {
        await this.routeFilterChild(filter, ev, out);
      }

      const matchers = this.matchers.get(parentKey);
      if (matchers !== undefined) {
        for (const m of matchers) {
          const seg = this.query.segments[m.seg] as SimpleSegment;
          if (matchStreaming(seg, key, parentKind)) {
            await this.advance(ev, m.seg + 1, out);
          }
        }
      }

      if (this.recMatchers.length > 0) {
        const parentPath = ev.path.slice(0, -1);
        for (const rm of this.recMatchers) {
          if (isPrefix(rm.base, parentPath)) {
            const inner = (this.query.segments[rm.seg] as { kind: "recursive"; inner: SimpleSegment }).inner;
            if (!needsStaging(inner) && matchStreaming(inner, key, parentKind)) {
              await this.advance(ev, rm.seg + 1, out);
            }
          }
        }
      }
    }

    if (ev.type === "open") {
      if (this.recMatchers.length > 0) {
        for (const rm of this.recMatchers) {
          const inner = (this.query.segments[rm.seg] as { kind: "recursive"; inner: SimpleSegment }).inner;
          if (needsStaging(inner) && isPrefix(rm.base, ev.path)) {
            this.registerInstrument(ev.path, [inner, ...this.query.segments.slice(rm.seg + 1)]);
          }
        }
      }
    }

    if (this.recorders.size > 0) {
      // Recorders are based at open containers, i.e. at ancestors of the
      // current event (or at the event node itself for open/close events).
      const maxDepth = ev.type === "value" ? depth - 1 : depth;
      for (let k = 0; k <= maxDepth; k++) {
        const rec = this.recorders.get(this.keyStack[k]);
        if (rec === undefined) continue;
        await rec.push(ev);
        if (rec.isDone(ev)) {
          this.recorders.delete(rec.key);
          const results = await rec.finalize();
          for (const r of results) this.emitDedup(r, out);
        }
      }
    }

    if (ev.type === "close") {
      const k = this.keyStack[depth];
      this.keyStack.length = depth;
      this.kindStack.length = depth;
      this.matchers.delete(k);
      this.filters.delete(k);
      this.instrumentKeys.delete(k);
      if (this.recMatchers.length > 0) {
        this.recMatchers = this.recMatchers.filter((rm) => !pathEquals(rm.base, ev.path));
      }
    }
    return out;
  }

  /** Node at ev.path matched segments[0..seg); process segments[seg]. */
  private async advance(ev: TreeEvent, seg: number, out: QueryResult[]): Promise<void> {
    const q = ev.path;
    if (seg === this.query.segments.length) {
      if (ev.type === "value") {
        this.emitDedup({ path: [...q], value: ev.value }, out);
      } else {
        this.addRecorder(q, (value) => [{ path: [...q], value }]);
      }
      return;
    }
    const segment = this.query.segments[seg];
    if (segment.kind === "recursive") {
      if (ev.type === "open") {
        this.recMatchers.push({ seg, base: [...q] });
        if (needsStaging(segment.inner)) {
          this.registerInstrument(q, [segment.inner, ...this.query.segments.slice(seg + 1)]);
        }
      }
      return;
    }
    if (needsStaging(segment)) {
      if (ev.type === "open") {
        this.registerInstrument(q, this.query.segments.slice(seg));
      }
      return;
    }
    if (ev.type === "open") {
      const key = this.keyStack[q.length];
      const list = this.matchers.get(key);
      const matcher = { seg };
      if (list === undefined) this.matchers.set(key, [matcher]);
      else list.push(matcher);
    }
  }

  private registerInstrument(path: PathSeg[], segments: Segment[]): void {
    const first = segments[0] as SimpleSegment;
    const pathKey = this.keyStack[path.length];
    const segKey = segmentKey(first);
    let seen = this.instrumentKeys.get(pathKey);
    if (seen === undefined) {
      seen = new Set();
      this.instrumentKeys.set(pathKey, seen);
    }
    if (seen.has(segKey)) return;
    seen.add(segKey);

    if (first.kind === "filter") {
      this.filters.set(pathKey, { path: [...path], rest: segments });
    } else {
      // Negative index / slice: stage the whole container, then evaluate the
      // remaining segments on the rebuilt value (tail positions are only
      // known once the container closes).
      this.addRecorder(path, (value) =>
        evaluateSegments(value, [...path], segments, this.budget));
    }
  }

  private async routeFilterChild(
    inst: FilterInstrument,
    ev: TreeEvent,
    out: QueryResult[],
  ): Promise<void> {
    const filterSeg = inst.rest[0] as SimpleSegment & { kind: "filter" };
    if (ev.type === "value") {
      if (!exprPasses(filterSeg.expr, ev.value, this.budget)) return;
      if (inst.rest.length === 1) {
        this.emitDedup({ path: [...ev.path], value: ev.value }, out);
      } else {
        for (const r of evaluateSegments(ev.value, ev.path, inst.rest.slice(1), this.budget)) {
          this.emitDedup(r, out);
        }
      }
      return;
    }
    if (ev.type === "open") {
      const childPath = [...ev.path];
      this.addRecorder(childPath, (value) => {
        if (!exprPasses(filterSeg.expr, value, this.budget)) return [];
        if (inst.rest.length === 1) return [{ path: childPath, value }];
        return evaluateSegments(value, childPath, inst.rest.slice(1), this.budget);
      });
    }
  }

  private addRecorder(
    path: PathSeg[],
    onClose: (value: JsonValue) => QueryResult[] | Promise<QueryResult[]>,
  ): void {
    const key = this.keyStack[path.length];
    if (this.recorders.has(key)) return;
    this.recorders.set(key, new EventRecorder(key, path, this.bufferLimit, this.store, onClose));
  }

  private emitDedup(result: QueryResult, out: QueryResult[]): void {
    const key = encodePath(result.path);
    if (this.emitted.has(key)) return;
    this.emitted.add(key);
    out.push(result);
  }

  async dispose(): Promise<void> {
    const pending = [...this.recorders.values()];
    this.recorders.clear();
    for (const rec of pending) await rec.dispose();
    await this.store.disposeAll();
  }
}

async function* toAsyncIterable(
  events: Iterable<TreeEvent> | AsyncIterable<TreeEvent>,
): AsyncGenerator<TreeEvent> {
  yield* events as AsyncIterable<TreeEvent>;
}

/**
 * Streaming query execution over a tree event stream.
 *
 * - Results that only need a forward look (fields, wildcards, non-negative
 *   indices/slices, recursive descent) are emitted as soon as their node is
 *   reached (scalars immediately, containers when their subtree closes).
 * - Filters stage one candidate element at a time; negative indices and
 *   slices stage the whole container, because tail positions are unknown
 *   until the container closes. Staging is bounded by `bufferLimit` events
 *   and then explicitly spilled to disk.
 * - Duplicate hits on the same path (e.g. via recursive descent) are
 *   emitted only once.
 */
export async function* streamQuery(
  events: Iterable<TreeEvent> | AsyncIterable<TreeEvent>,
  query: Query,
  opts: StreamOptions = {},
): AsyncGenerator<QueryResult> {
  const engine = new StreamEngine(query, opts);
  try {
    for await (const ev of toAsyncIterable(events)) {
      const out = await engine.handle(ev);
      for (const r of out) yield r;
    }
  } finally {
    await engine.dispose();
  }
}

/** Convenience: collect all streaming results into an array. */
export async function collectStream(
  events: Iterable<TreeEvent> | AsyncIterable<TreeEvent>,
  query: Query,
  opts: StreamOptions = {},
): Promise<QueryResult[]> {
  const out: QueryResult[] = [];
  for await (const r of streamQuery(events, query, opts)) out.push(r);
  return out;
}
