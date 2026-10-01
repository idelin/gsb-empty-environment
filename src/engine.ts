import type { JsonValue } from "./json.ts";
import { pointerOf } from "./json.ts";
import { TemplateError } from "./errors.ts";
import { builtins } from "./functions.ts";
import type { FnImpl } from "./functions.ts";
import { parseTemplate } from "./template.ts";
import type { ParsedTemplate } from "./template.ts";
import { DataError } from "./errors.ts";
import { Store, isArr, splitPath } from "./store.ts";
import type { SymSeg } from "./store.ts";
import { render } from "./render.ts";
import type { Fragment, RenderContext, RenderOut, RenderStats } from "./render.ts";

export type MissingPolicy = "empty" | "null" | "error";

export interface Budget {
  /** Total loop-iteration + partial-ref expansions allowed per render (shared). */
  maxExpansions: number;
  /** Max template recursion depth (guards recursive partials). */
  maxDepth: number;
  /** Max nodes in the published output tree (guards output explosion). */
  maxOutputNodes: number;
}

export interface EngineOptions {
  onMissing?: MissingPolicy;
  budget?: Partial<Budget>;
  /** Extra whitelisted functions on top of the built-ins. */
  functions?: Record<string, FnImpl>;
  /** Named subtemplates usable via { "$ref": "name" }. */
  partials?: Record<string, JsonValue>;
}

export interface ProvenanceEntry {
  /** JSON pointer into the output. */
  out: string;
  /** Template node id (JSON pointer into the template, "@partial/..." for partials). */
  template: string;
  /** JSON pointers into the input data. */
  input: string[];
}

export interface PublishResult {
  output: JsonValue;
  provenance: ProvenanceEntry[];
}

/** Transactional data operations. All path segments are concrete (indices). */
export interface Tx {
  set(path: string, value: JsonValue): void;
  insert(path: string, index: number, value: JsonValue): void;
  remove(path: string, index: number): void;
  move(path: string, from: number, to: number): void;
}

const DEFAULT_BUDGET: Budget = {
  maxExpansions: 100000,
  maxDepth: 100,
  maxOutputNodes: 1000000,
};

function countNodes(v: JsonValue): number {
  if (v === null || typeof v !== "object") return 1;
  let n = 1;
  if (Array.isArray(v)) {
    for (const item of v) n += countNodes(item);
  } else {
    for (const k of Object.keys(v)) n += countNodes(v[k]);
  }
  return n;
}

export class TemplateEngine {
  private store: Store;
  private parsed: ParsedTemplate;
  private onMissing: MissingPolicy;
  private budget: Budget;
  private functions: Map<string, FnImpl>;
  private cache: Map<string, Fragment>;
  private published: PublishResult | null;
  /** Stats of the most recent render (cache hits/misses, expansions used). */
  stats: RenderStats;

  constructor(template: JsonValue, options: EngineOptions = {}) {
    this.onMissing = options.onMissing ?? "empty";
    this.budget = { ...DEFAULT_BUDGET, ...(options.budget ?? {}) };
    this.functions = new Map(Object.entries(builtins));
    if (options.functions !== undefined) {
      for (const name of Object.keys(options.functions)) {
        this.functions.set(name, options.functions[name]);
      }
    }
    this.parsed = parseTemplate(template, new Set(this.functions.keys()), options.partials);
    this.store = new Store();
    this.cache = new Map();
    this.published = null;
    this.stats = { hits: 0, misses: 0, expansions: 0 };
  }

  /** Replace all input data (fresh entity ids) and publish a full render. */
  setData(json: JsonValue): PublishResult {
    this.store = new Store();
    this.store.root = this.store.wrap(json);
    this.cache = new Map();
    return this.publish(false);
  }

  /** Current input data as plain JSON. */
  getData(): JsonValue {
    return this.store.toJson(this.store.root);
  }

  /**
   * Apply data operations and publish output + provenance atomically.
   * Any failure (bad op, render error, function failure, budget exceeded)
   * rolls the data back and keeps the previously published result.
   */
  transaction(fn: (tx: Tx) => void): PublishResult {
    const snap = this.store.snapshot();
    try {
      fn(this.store);
      return this.publish(true);
    } catch (err) {
      this.store.restore(snap);
      throw err;
    }
  }

  /** Full re-render of current data bypassing the fragment cache (for verification). */
  fullRender(): PublishResult {
    const ctx = this.makeContext(null, null);
    const r = render(this.parsed.root, ctx, 0);
    return this.finalize(r);
  }

  get output(): JsonValue {
    if (this.published === null) throw new TemplateError("nothing published yet");
    return this.published.output;
  }

  get provenance(): ProvenanceEntry[] {
    if (this.published === null) throw new TemplateError("nothing published yet");
    return this.published.provenance;
  }

  /** Stable entity ids of the array at `path`, in current order (debugging/verification). */
  entityIds(path: string): number[] {
    const r = this.store.resolvePath(splitPath(path));
    if (!r.found || !isArr(r.value)) {
      throw new DataError(`entityIds: "${path}" is not an array`, path);
    }
    return r.value.entries.map((e) => e.eid);
  }

  private makeContext(
    cache: ReadonlyMap<string, Fragment> | null,
    nextCache: Map<string, Fragment> | null,
  ): RenderContext {
    this.stats = { hits: 0, misses: 0, expansions: 0 };
    return {
      store: this.store,
      scopes: [],
      onMissing: this.onMissing,
      functions: this.functions,
      partials: this.parsed.partials,
      cache,
      nextCache,
      budget: { expansionsLeft: this.budget.maxExpansions },
      maxDepth: this.budget.maxDepth,
      stats: this.stats,
    };
  }

  private publish(incremental: boolean): PublishResult {
    const nextCache = new Map<string, Fragment>();
    const ctx = this.makeContext(incremental ? this.cache : null, nextCache);
    const r = render(this.parsed.root, ctx, 0);
    const result = this.finalize(r);
    this.cache = nextCache;
    this.published = result;
    return result;
  }

  private finalize(r: RenderOut): PublishResult {
    const nodes = countNodes(r.value);
    if (nodes > this.budget.maxOutputNodes) {
      throw new TemplateError(
        `output budget exceeded (${nodes} nodes > ${this.budget.maxOutputNodes})`,
        this.parsed.root.id,
      );
    }
    const provenance: ProvenanceEntry[] = r.prov.map((p) => ({
      out: pointerOf(p.out),
      template: p.template,
      input: p.input.map((sym) => this.symPointer(sym)),
    }));
    return { output: r.value, provenance };
  }

  /** Convert a symbolic path to a concrete JSON pointer against current data. */
  private symPointer(sym: SymSeg[]): string {
    const r = this.store.resolveSym(sym);
    if (r.concrete.length === sym.length) return pointerOf(r.concrete);
    const rest = sym.slice(r.concrete.length).map((s) => ("k" in s ? s.k : `#${s.e}`));
    return pointerOf([...r.concrete, ...rest]);
  }
}
