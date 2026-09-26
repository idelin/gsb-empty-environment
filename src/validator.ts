import {
  deleteAt,
  getAt,
  isPrefixOf,
  parentPath,
  pathDepth,
  pathsRelated,
  ROOT,
  setAt,
  type Path,
} from './paths.ts';
import { expandNode, schemaAtPath, type RuleInstance, type ValidationError } from './expand.ts';
import type { SchemaNode } from './schema.ts';

/** Pass as the value of `update()` to delete the node at the given path. */
export const DELETE: unique symbol = Symbol('DELETE');

export interface ChangeSet {
  /** Errors present after the change that were not present before. */
  added: ValidationError[];
  /** Errors present before the change that are gone afterwards. */
  removed: ValidationError[];
}

function errorKey(e: ValidationError): string {
  return `${e.instancePath}\n${e.schemaPath}\n${e.message}`;
}

function sortErrors(errors: ValidationError[]): ValidationError[] {
  return errors.sort(
    (a, b) => a.instancePath.localeCompare(b.instancePath) || a.schemaPath.localeCompare(b.schemaPath),
  );
}

/**
 * Validates a mutable JSON-like document against a schema, incrementally.
 *
 * The schema is compiled into instance-bound rules, each declaring the
 * instance paths it reads (`deps`). A dependency index maps instance paths
 * to the rules reading them, so a local write at path P only re-runs rules
 * whose dependencies contain P, contain an ancestor of P, or live under P.
 * Rules anchored below a deleted subtree are dropped, which retracts their
 * errors; rules are re-expanded from the schema so repaired nodes regain
 * their constraints.
 */
export class IncrementalValidator {
  readonly #schema: SchemaNode;
  #doc: unknown;
  readonly #rules = new Map<string, RuleInstance>();
  readonly #depIndex = new Map<Path, Set<string>>();
  readonly #errors = new Map<string, ValidationError>();
  /** Ids of the rules executed by the most recent mutation (observability). */
  lastExecuted: string[] = [];

  constructor(schema: SchemaNode, doc: unknown) {
    this.#schema = schema;
    this.#doc = undefined;
    this.replaceDocument(doc);
  }

  getDocument(): unknown {
    return this.#doc;
  }

  /** Number of live instance-bound rules (for inspection/tests). */
  get ruleCount(): number {
    return this.#rules.size;
  }

  /** Current incremental error set. */
  getErrors(): ValidationError[] {
    return sortErrors([...this.#errors.values()]);
  }

  /** Independent full revalidation from scratch (ground truth). */
  validateFull(): ValidationError[] {
    const fresh: RuleInstance[] = [];
    expandNode(this.#schema, '#', this.#doc, ROOT, (r) => fresh.push(r));
    const errors: ValidationError[] = [];
    for (const rule of fresh) {
      const err = rule.run(this.#doc);
      if (err) errors.push(err);
    }
    return sortErrors(errors);
  }

  /** Replace the whole document and validate it from scratch. */
  replaceDocument(doc: unknown): ValidationError[] {
    this.#doc = doc;
    this.#rules.clear();
    this.#depIndex.clear();
    this.#errors.clear();
    const fresh: RuleInstance[] = [];
    expandNode(this.#schema, '#', doc, ROOT, (r) => fresh.push(r));
    for (const rule of fresh) this.#register(rule);
    this.lastExecuted = fresh.map((r) => r.id);
    for (const rule of fresh) {
      const err = rule.run(doc);
      if (err) this.#errors.set(rule.id, err);
    }
    return this.getErrors();
  }

  /** Set the value at `path` (pass DELETE to remove it) and revalidate. */
  update(path: Path, value: unknown): ChangeSet {
    if (value === DELETE) return this.remove(path);
    const before = this.#snapshot();
    const revalPath = this.#revalidationPath(path);
    setAt(this.#doc, path, value);
    this.#revalidate(revalPath);
    return this.#diff(before);
  }

  /** Remove the node at `path` and revalidate. */
  remove(path: Path): ChangeSet {
    const before = this.#snapshot();
    const revalPath = this.#revalidationPath(path);
    deleteAt(this.#doc, path);
    this.#revalidate(revalPath);
    return this.#diff(before);
  }

  /**
   * Writing or deleting an array element shifts every later sibling's index,
   * so element-level changes inside an array are revalidated from the array
   * itself; object property changes stay precisely scoped.
   */
  #revalidationPath(path: Path): Path {
    const parent = parentPath(path);
    if (parent !== null && Array.isArray(getAt(this.#doc, parent))) return parent;
    return path;
  }

  #register(rule: RuleInstance): void {
    this.#rules.set(rule.id, rule);
    for (const dep of rule.deps) {
      let bucket = this.#depIndex.get(dep);
      if (!bucket) this.#depIndex.set(dep, (bucket = new Set()));
      bucket.add(rule.id);
    }
  }

  #unregister(rule: RuleInstance): void {
    this.#rules.delete(rule.id);
    for (const dep of rule.deps) {
      const bucket = this.#depIndex.get(dep);
      if (!bucket) continue;
      bucket.delete(rule.id);
      if (bucket.size === 0) this.#depIndex.delete(dep);
    }
  }

  #revalidate(path: Path): void {
    // 1. Find every live rule that might read the changed region.
    //    Shallow deps: only a write at or above the dep can change the read.
    //    Deep deps (enum): any write inside the dep may change the read.
    const affected = new Set<string>();
    for (const [dep, ids] of this.#depIndex) {
      for (const id of ids) {
        const rule = this.#rules.get(id);
        if (!rule) continue;
        const hit = rule.deep ? pathsRelated(dep, path) : isPrefixOf(path, dep);
        if (hit) affected.add(id);
      }
    }

    // 2. Drop those rules and their errors; remember where they were anchored.
    const scopes = new Set<Path>();
    for (const id of affected) {
      const rule = this.#rules.get(id);
      if (!rule) continue;
      scopes.add(rule.scope);
      this.#unregister(rule);
      this.#errors.delete(id);
    }
    // A write may create a node that had no rules before (e.g. a new object
    // key); re-expanding from the parent scope picks up rules for new nodes.
    // Already-live rules are skipped and are NOT re-executed.
    const parent = parentPath(path);
    if (parent !== null) scopes.add(parent);

    // 3. Re-expand rules from the schema at the affected scopes, parents
    //    first. Already-live rules are kept as-is and are NOT re-executed.
    const newRules: RuleInstance[] = [];
    const sortedScopes = [...scopes].sort((a, b) => pathDepth(a) - pathDepth(b));
    for (const scope of sortedScopes) {
      const loc = schemaAtPath(this.#schema, scope);
      if (!loc) continue;
      expandNode(loc.node, loc.sPath, getAt(this.#doc, scope), scope, (rule) => {
        if (this.#rules.has(rule.id)) return;
        this.#register(rule);
        newRules.push(rule);
      });
    }

    // 4. Only the (re)created rules are executed.
    this.lastExecuted = newRules.map((r) => r.id);
    for (const rule of newRules) {
      const err = rule.run(this.#doc);
      if (err) this.#errors.set(rule.id, err);
    }
  }

  #snapshot(): Map<string, ValidationError> {
    const snap = new Map<string, ValidationError>();
    for (const err of this.#errors.values()) snap.set(errorKey(err), err);
    return snap;
  }

  #diff(before: Map<string, ValidationError>): ChangeSet {
    const after = this.#snapshot();
    const added: ValidationError[] = [];
    const removed: ValidationError[] = [];
    for (const [key, err] of after) if (!before.has(key)) added.push(err);
    for (const [key, err] of before) if (!after.has(key)) removed.push(err);
    return { added: sortErrors(added), removed: sortErrors(removed) };
  }
}
