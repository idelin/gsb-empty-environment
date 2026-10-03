import { parsePath, deepEqual, type JsonValue, type PathSeg } from "./json.ts";
import { Store, newChangeSet, type ChangeSet, type Undo } from "./store.ts";
import { compileTemplate, type TNode, type MissingPolicy } from "./template.ts";
import { builtinFunctions, type FnImpl } from "./functions.ts";
import {
  collect,
  refresh,
  renderNode,
  type Ctx,
  type Fragment,
  type ProvEntry,
} from "./render.ts";

export interface EngineOptions {
  /** Default policy for missing input paths (explicit null is NOT missing). Default: "error". */
  onMissing?: MissingPolicy;
  /** Total loop-iteration + function-call budget per render/transaction. Default: 10000. */
  maxExpansions?: number;
  /** Output node budget per render/transaction (output explosion guard). Default: 100000. */
  maxOutputNodes?: number;
  /** Template nesting depth limit (recursive template guard). Default: 200. */
  maxDepth?: number;
  /** Extra whitelisted functions (merged over the builtins). */
  functions?: Record<string, FnImpl>;
}

interface Limits {
  maxExpansions: number;
  maxOutputNodes: number;
  maxDepth: number;
}

export class Engine {
  readonly store: Store;
  private readonly tmpl: TNode;
  private readonly onMissing: MissingPolicy;
  private readonly fns: Record<string, FnImpl>;
  private readonly limits: Limits;
  private rootFrag: Fragment;
  /** Last published output. */
  output: JsonValue;
  /** Last published dual provenance table: output path -> { template path, input paths }. */
  provenance: Record<string, ProvEntry>;

  constructor(template: JsonValue, data: JsonValue, opts: EngineOptions = {}) {
    this.tmpl = compileTemplate(template);
    this.store = new Store(data);
    this.onMissing = opts.onMissing ?? "error";
    this.fns = { ...builtinFunctions, ...(opts.functions ?? {}) };
    this.limits = {
      maxExpansions: opts.maxExpansions ?? 10_000,
      maxOutputNodes: opts.maxOutputNodes ?? 100_000,
      maxDepth: opts.maxDepth ?? 200,
    };
    const r = this.renderFresh();
    this.rootFrag = r.frag;
    this.output = r.output;
    this.provenance = r.provenance;
  }

  private makeCtx(dirty: ChangeSet | null): Ctx {
    return {
      store: this.store,
      cfg: { onMissing: this.onMissing, functions: this.fns },
      budget: {
        expansionsLeft: this.limits.maxExpansions,
        outputLeft: this.limits.maxOutputNodes,
        maxDepth: this.limits.maxDepth,
      },
      dirty,
    };
  }

  private renderFresh(): { frag: Fragment; output: JsonValue; provenance: Record<string, ProvEntry> } {
    const ctx = this.makeCtx(null);
    const frag = renderNode(this.tmpl, { vars: new Map() }, ctx, 0);
    const { output, provenance } = collect(frag, this.store);
    return { frag, output, provenance };
  }

  /** Full re-render from scratch (ground truth for verifying incremental updates). */
  fullRender(): { output: JsonValue; provenance: Record<string, ProvEntry> } {
    const r = this.renderFresh();
    return { output: r.output, provenance: r.provenance };
  }

  /** True when the incrementally maintained state matches a full re-render. */
  verify(): boolean {
    const full = this.fullRender();
    return deepEqual(this.output, full.output) && provenanceEqual(this.provenance, full.provenance);
  }

  /** The live fragment tree (exposed for introspection/incrementality checks). */
  get rootFragment(): Fragment {
    return this.rootFrag;
  }

  begin(): Tx {
    return new Tx(this);
  }

  /** Publishes a transaction's mutations incrementally. Throws -> caller rolls back. */
  commitTx(cs: ChangeSet): { output: JsonValue; provenance: Record<string, ProvEntry> } {
    const ctx = this.makeCtx(cs);
    const frag = refresh(this.rootFrag, ctx);
    const { output, provenance } = collect(frag, this.store);
    this.rootFrag = frag;
    this.output = output;
    this.provenance = provenance;
    return { output, provenance };
  }
}

export function provenanceEqual(
  a: Record<string, ProvEntry>,
  b: Record<string, ProvEntry>,
): boolean {
  const ka = Object.keys(a);
  const kb = Object.keys(b);
  if (ka.length !== kb.length) return false;
  for (const k of ka) {
    const ea = a[k];
    const eb = b[k];
    if (!eb) return false;
    if (ea.template !== eb.template) return false;
    if (ea.inputs.length !== eb.inputs.length) return false;
    for (let i = 0; i < ea.inputs.length; i++) if (ea.inputs[i] !== eb.inputs[i]) return false;
  }
  return true;
}

/**
 * A transaction batches input mutations and publishes output + provenance
 * atomically. Any failure (function error, missing path, budget) rolls the
 * input store back and leaves the previously published state untouched.
 */
export class Tx {
  private readonly engine: Engine;
  private readonly undos: Undo[] = [];
  private readonly cs: ChangeSet = newChangeSet();
  private closed = false;

  constructor(engine: Engine) {
    this.engine = engine;
  }

  private ensureOpen(): void {
    if (this.closed) throw new Error("transaction already closed");
  }

  set(path: string | PathSeg[], value: JsonValue): Tx {
    this.ensureOpen();
    this.undos.push(this.engine.store.set(parsePath(path), value, this.cs));
    return this;
  }

  insert(path: string | PathSeg[], index: number, value: JsonValue): Tx {
    this.ensureOpen();
    this.undos.push(this.engine.store.insert(parsePath(path), index, value, this.cs));
    return this;
  }

  remove(path: string | PathSeg[]): Tx {
    this.ensureOpen();
    this.undos.push(this.engine.store.remove(parsePath(path), this.cs));
    return this;
  }

  move(path: string | PathSeg[], from: number, to: number): Tx {
    this.ensureOpen();
    this.undos.push(this.engine.store.move(parsePath(path), from, to, this.cs));
    return this;
  }

  commit(): { output: JsonValue; provenance: Record<string, ProvEntry> } {
    this.ensureOpen();
    try {
      const r = this.engine.commitTx(this.cs);
      this.closed = true;
      return r;
    } catch (err) {
      for (let i = this.undos.length - 1; i >= 0; i--) this.undos[i]();
      this.closed = true;
      throw err;
    }
  }

  rollback(): void {
    this.ensureOpen();
    for (let i = this.undos.length - 1; i >= 0; i--) this.undos[i]();
    this.closed = true;
  }
}
