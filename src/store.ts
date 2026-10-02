import type { Graph } from './compiler.ts';
import {
  collectFrom,
  errorKey,
  validate,
  type ValidationError,
  type ValidationResult,
  type ValidateOptions,
  type Verdict,
} from './validator.ts';

function parsePointer(path: string): string[] {
  if (path === '' || path === '/') return [];
  if (!path.startsWith('/')) throw new Error(`invalid instance path "${path}"`);
  return path
    .slice(1)
    .split('/')
    .map((s) => s.replace(/~1/g, '/').replace(/~0/g, '~'));
}

function escapeSegment(seg: string): string {
  return seg.replace(/~/g, '~0').replace(/\//g, '~1');
}

function getAt(value: unknown, segs: string[]): unknown {
  let cur = value;
  for (const seg of segs) {
    if (cur === null || typeof cur !== 'object') return undefined;
    cur = (cur as Record<string, unknown>)[seg];
  }
  return cur;
}

/**
 * Holds a mutable instance plus its validation errors and a dependency index
 * (instancePath -> errors raised at that path). Incremental edits revalidate
 * only the affected region, then swap errors and index in a single commit.
 */
export class InstanceStore {
  private root: unknown;
  private errs: ValidationError[] = [];
  private index = new Map<string, ValidationError[]>();
  private lastResult: ValidationResult;
  private readonly graph: Graph;
  private readonly opts: ValidateOptions;

  constructor(graph: Graph, initial: unknown, opts: ValidateOptions = {}) {
    this.graph = graph;
    this.opts = opts;
    this.root = initial;
    this.lastResult = validate(this.graph, this.root, this.opts);
    this.commitAll(this.lastResult.errors);
  }

  get value(): unknown {
    return this.root;
  }

  get errors(): readonly ValidationError[] {
    return this.errs;
  }

  get verdict(): Verdict {
    return this.lastResult.verdict;
  }

  get result(): ValidationResult {
    return this.lastResult;
  }

  /** Dependency index lookup: all current errors raised at exactly this instance path. */
  errorsAt(instancePath: string): readonly ValidationError[] {
    return this.index.get(instancePath) ?? [];
  }

  set(path: string, value: unknown): ValidationResult {
    return this.update(path, value, false);
  }

  remove(path: string): ValidationResult {
    return this.update(path, undefined, true);
  }

  private update(path: string, value: unknown, isDelete: boolean): ValidationResult {
    const segs = parsePointer(path);
    if (segs.length === 0) {
      if (isDelete) throw new Error('cannot delete the root instance');
      this.root = value;
      const res = validate(this.graph, this.root, this.opts);
      this.commitAll(res.errors);
      this.lastResult = res;
      return res;
    }

    const containerSegs = segs.slice(0, -1);
    const key = segs[segs.length - 1];
    const container = getAt(this.root, containerSegs);
    if (container === null || typeof container !== 'object') {
      throw new Error(`no container at instance path "${path}"`);
    }

    // 1. Apply the mutation to the live instance.
    if (Array.isArray(container)) {
      const i = Number(key);
      if (!Number.isInteger(i) || i < 0 || (isDelete ? i >= container.length : i > container.length)) {
        throw new Error(`invalid array index "${key}" at "${path}"`);
      }
      if (isDelete) container.splice(i, 1);
      else container[i] = value;
    } else {
      if (isDelete) delete (container as Record<string, unknown>)[key];
      else (container as Record<string, unknown>)[key] = value;
    }

    // 2. Find the revalidation root: the outermost oneOf on the path (its
    //    branch choice may flip), otherwise the modified container itself.
    const anchor = this.locate(containerSegs);

    // 3. Revalidate only the affected region.
    let newErrors: ValidationError[] = [];
    let incomplete = false;
    let incompleteReason: string | undefined;
    if (anchor.nodeId !== undefined) {
      const depth = anchor.path === '' ? 0 : anchor.path.split('/').length - 1;
      const r = collectFrom(this.graph, anchor.nodeId, getAt(this.root, parsePointer(anchor.path)), anchor.path, depth, this.opts);
      newErrors = r.errors;
      incomplete = r.incomplete;
      incompleteReason = r.incompleteReason;
    }

    // 4. Atomic commit: swap errors and dependency index together.
    const res = this.commitRegion(anchor.path, newErrors, incomplete, incompleteReason);
    this.lastResult = res;
    return res;
  }

  /**
   * Walks from the graph root to the given instance location, returning the
   * node to revalidate from. If any oneOf sits on the chain, the outermost
   * one is returned (a child edit can flip which branch matches).
   */
  private locate(containerSegs: string[]): { nodeId: number | undefined; path: string } {
    let nodeId: number | undefined = this.graph.root;
    let value: unknown = this.root;
    let path = '';
    let anchor: { nodeId: number | undefined; path: string } | null = null;

    let i = 0;
    while (i < containerSegs.length) {
      if (nodeId === undefined) break;
      const node = this.graph.nodes[nodeId];
      if (node.kind === 'union') {
        anchor ??= { nodeId, path };
        const chosen = this.pickBranch(node.options, value, path);
        if (chosen === undefined) return anchor;
        nodeId = chosen;
        continue; // union edges consume no instance segment
      }
      const seg = containerSegs[i];
      if (node.kind === 'object') {
        nodeId = node.props.get(seg); // may be undefined: unconstrained subtree
      } else if (node.kind === 'array') {
        nodeId = node.items;
      } else {
        // scalar with remaining segments: schema/instance shape mismatch
        return anchor ?? { nodeId, path };
      }
      value = getAt(value, [seg]);
      path = `${path}/${escapeSegment(seg)}`;
      i++;
    }
    return anchor ?? { nodeId, path };
  }

  private pickBranch(options: readonly number[], value: unknown, path: string): number | undefined {
    let chosen: number | undefined;
    let matches = 0;
    const depth = path === '' ? 0 : path.split('/').length - 1;
    for (const opt of options) {
      const r = collectFrom(this.graph, opt, value, path, depth, this.opts);
      if (!r.incomplete && r.errors.length === 0) {
        matches++;
        chosen = opt;
      }
    }
    return matches === 1 ? chosen : undefined;
  }

  private commitAll(errors: ValidationError[]): void {
    const dedup = new Map<string, ValidationError>();
    for (const e of errors) {
      const key = errorKey(e);
      if (!dedup.has(key)) dedup.set(key, e);
    }
    this.errs = [...dedup.values()];
    const nextIndex = new Map<string, ValidationError[]>();
    for (const e of this.errs) {
      const bucket = nextIndex.get(e.instancePath);
      if (bucket) bucket.push(e);
      else nextIndex.set(e.instancePath, [e]);
    }
    this.index = nextIndex;
  }

  private commitRegion(
    prefix: string,
    newErrors: ValidationError[],
    incomplete: boolean,
    incompleteReason?: string,
  ): ValidationResult {
    const inRegion = (p: string): boolean =>
      prefix === '' ? true : p === prefix || p.startsWith(`${prefix}/`);
    // Compute the next state fully, then swap: errors and index change together.
    const kept = this.errs.filter((e) => !inRegion(e.instancePath));
    this.commitAll([...kept, ...newErrors]);
    const verdict: Verdict = incomplete ? 'incomplete' : this.errs.length > 0 ? 'invalid' : 'valid';
    return { verdict, errors: this.errs, truncated: false, incompleteReason };
  }
}
