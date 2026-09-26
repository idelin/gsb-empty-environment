import {
  compileSchema,
  isArrayIndex,
  isPlainObject,
  type Check,
  type Json,
  type SchemaNode,
  type ValidationError,
} from "./checks.ts";
import type { DepIndex } from "./depindex.ts";
import { formatPointer, parsePointer, type Segments } from "./paths.ts";

export type PatchOp =
  | { op: "set"; path: string; value: Json }
  | { op: "remove"; path: string };

export interface PatchResult {
  /** Errors introduced by this patch. */
  added: ValidationError[];
  /** Errors retracted by this patch. */
  removed: ValidationError[];
  /** The full current error set after the patch. */
  errors: ValidationError[];
  /** Schema paths of the checks that were re-evaluated (observability). */
  revalidated: string[];
}

export function errorKey(e: ValidationError): string {
  return [e.instancePath, e.schemaPath, e.code, e.message].join("\n");
}

export function sortErrors(errors: ValidationError[]): ValidationError[] {
  return [...errors].sort((a, b) => (errorKey(a) < errorKey(b) ? -1 : errorKey(a) > errorKey(b) ? 1 : 0));
}

/** One-shot full validation; used as the oracle for consistency checks. */
export function validate(schema: SchemaNode, data: Json): ValidationError[] {
  const { checks } = compileSchema(schema);
  return sortErrors(checks.flatMap((c) => c.evaluate(data)));
}

export class IncrementalValidator {
  private schema: SchemaNode;
  private checks: Check[];
  private index: DepIndex;
  private errorsByCheck: Map<number, Map<string, ValidationError>>;
  private data: Json;
  /** Number of evaluations per check id, including the initial full pass. */
  readonly evalCounts: number[];

  constructor(schema: SchemaNode, data: Json) {
    this.schema = schema;
    const compiled = compileSchema(schema);
    this.checks = compiled.checks;
    this.index = compiled.index;
    this.data = data;
    this.evalCounts = this.checks.map(() => 0);
    this.errorsByCheck = new Map();
    for (const check of this.checks) {
      this.errorsByCheck.set(check.id, keyed(check.evaluate(this.data)));
      this.evalCounts[check.id]++;
    }
  }

  getData(): Json {
    return this.data;
  }

  getErrors(): ValidationError[] {
    const all = [...this.errorsByCheck.values()].flatMap((m) => [...m.values()]);
    return sortErrors(all);
  }

  /** Schema path of each compiled check, indexed by check id. */
  checkPaths(): string[] {
    return this.checks.map((c) => c.schemaPath);
  }

  apply(ops: PatchOp[]): PatchResult {
    const affected = new Set<number>();
    for (const op of ops) {
      const segs = parsePointer(op.path);
      const changed = op.op === "set" ? this.setAt(segs, op.value) : this.removeAt(segs);
      if (changed) {
        for (const id of this.index.query(segs)) affected.add(id);
      }
    }
    const added: ValidationError[] = [];
    const removed: ValidationError[] = [];
    for (const id of affected) {
      const check = this.checks[id];
      const next = keyed(check.evaluate(this.data));
      this.evalCounts[id]++;
      const prev = this.errorsByCheck.get(id)!;
      for (const [k, e] of next) if (!prev.has(k)) added.push(e);
      for (const [k, e] of prev) if (!next.has(k)) removed.push(e);
      this.errorsByCheck.set(id, next);
    }
    const revalidated = [...new Set([...affected].map((id) => this.checks[id].schemaPath))].sort();
    return {
      added: sortErrors(added),
      removed: sortErrors(removed),
      errors: this.getErrors(),
      revalidated,
    };
  }

  private setAt(segs: Segments, value: Json): boolean {
    if (segs.length === 0) {
      this.data = value;
      return true;
    }
    const parent = navigate(this.data, segs.slice(0, -1));
    const key = segs[segs.length - 1];
    if (isPlainObject(parent)) {
      parent[key] = value;
      return true;
    }
    if (Array.isArray(parent) && isArrayIndex(key) && Number(key) <= parent.length) {
      parent[Number(key)] = value;
      return true;
    }
    throw new Error(`cannot set ${formatPointer(segs)}: parent is missing or not a container`);
  }

  private removeAt(segs: Segments): boolean {
    if (segs.length === 0) throw new Error("cannot remove the document root");
    const parent = navigate(this.data, segs.slice(0, -1));
    const key = segs[segs.length - 1];
    if (isPlainObject(parent) && Object.prototype.hasOwnProperty.call(parent, key)) {
      delete parent[key];
      return true;
    }
    if (Array.isArray(parent) && isArrayIndex(key) && Number(key) < parent.length) {
      parent.splice(Number(key), 1);
      return true;
    }
    return false;
  }
}

function keyed(errors: ValidationError[]): Map<string, ValidationError> {
  return new Map(errors.map((e) => [errorKey(e), e]));
}

function navigate(root: Json, segs: Segments): Json | undefined {
  let cur: Json = root;
  for (const seg of segs) {
    if (isPlainObject(cur) && Object.prototype.hasOwnProperty.call(cur, seg)) {
      cur = cur[seg];
    } else if (Array.isArray(cur) && isArrayIndex(seg) && Number(seg) < cur.length) {
      cur = cur[Number(seg)];
    } else {
      return undefined;
    }
  }
  return cur;
}
