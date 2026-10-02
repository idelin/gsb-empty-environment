import type { Graph } from './compiler.ts';

export interface ValidationError {
  /** JSON-pointer into the instance, e.g. "/children/0/label" ('' is the root). */
  instancePath: string;
  /** JSON-pointer into the schema document, e.g. "#/$defs/tree/properties/label". */
  schemaPath: string;
  message: string;
  /** For oneOf failures: why each branch failed (best-effort, truncated). */
  causes?: ValidationError[];
}

export type Verdict = 'valid' | 'invalid' | 'incomplete';

export interface ValidationResult {
  verdict: Verdict;
  errors: ValidationError[];
  /** True when the error budget was exhausted. */
  truncated: boolean;
  incompleteReason?: string;
}

export interface ValidateOptions {
  /** Maximum instance nesting depth; exceeding it yields 'incomplete', never 'invalid'. */
  maxDepth?: number;
  /** Maximum number of errors collected; exhausting it yields 'incomplete'. */
  maxErrors?: number;
}

export const DEFAULT_MAX_DEPTH = 100;
export const DEFAULT_MAX_ERRORS = 1000;

/**
 * Dedup key: the instance path AND the schema path AND the message.
 * Two findings at the same instance location but produced by different
 * schema rules are distinct errors and must never be merged.
 */
export function errorKey(e: ValidationError): string {
  // NUL cannot appear in a JSON-pointer, so it is a collision-free separator.
  return [e.instancePath, e.schemaPath, e.message].join(String.fromCharCode(0));
}

interface Sink {
  readonly errors: ValidationError[];
  incomplete: boolean;
  incompleteReason?: string;
  /** True once the error budget is exhausted; validation unwinds early. */
  stop: boolean;
  add(e: ValidationError): void;
  flagIncomplete(reason: string): void;
}

class Collector implements Sink {
  readonly errors: ValidationError[] = [];
  incomplete = false;
  incompleteReason?: string;
  stop = false;
  private readonly seen = new Set<string>();
  private readonly maxErrors: number;

  constructor(maxErrors: number) {
    this.maxErrors = maxErrors;
  }

  add(e: ValidationError): void {
    if (this.stop) return;
    const key = errorKey(e);
    if (this.seen.has(key)) return;
    if (this.errors.length >= this.maxErrors) {
      this.stop = true;
      this.flagIncomplete(`error budget exhausted (maxErrors=${this.maxErrors})`);
      return;
    }
    this.seen.add(key);
    this.errors.push(e);
  }

  flagIncomplete(reason: string): void {
    if (!this.incomplete) {
      this.incomplete = true;
      this.incompleteReason = reason;
    }
  }
}

function describe(value: unknown): string {
  if (value === null) return 'null';
  if (Array.isArray(value)) return 'array';
  return typeof value;
}

function isPlainObject(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}

function joinPath(base: string, seg: string): string {
  return `${base}/${seg.replace(/~/g, '~0').replace(/\//g, '~1')}`;
}

function validateNode(
  graph: Graph,
  id: number,
  value: unknown,
  path: string,
  depth: number,
  maxDepth: number,
  stack: Set<string>,
  sink: Sink,
): void {
  if (sink.stop) return;
  if (depth > maxDepth) {
    sink.flagIncomplete(`instance depth limit exceeded (maxDepth=${maxDepth}) at ${path || '/'}`);
    return;
  }
  // Visit-state guard: never expand the same graph node at the same instance
  // location twice along one descent. Productive recursion always consumes
  // instance, so for finite instances this is only a safety net.
  const guard = `${id}@${path}`;
  if (stack.has(guard)) return;
  stack.add(guard);
  try {
    const node = graph.nodes[id];
    switch (node.kind) {
      case 'scalar': {
        const ok = node.scalar === 'null' ? value === null : typeof value === node.scalar;
        if (!ok) {
          sink.add({
            instancePath: path,
            schemaPath: node.schemaPath,
            message: `expected ${node.scalar}, got ${describe(value)}`,
          });
        }
        return;
      }
      case 'object': {
        if (!isPlainObject(value)) {
          sink.add({
            instancePath: path,
            schemaPath: node.schemaPath,
            message: `expected object, got ${describe(value)}`,
          });
          return;
        }
        for (const req of node.required) {
          if (!(req in value)) {
            sink.add({
              instancePath: path,
              schemaPath: node.schemaPath,
              message: `missing required property "${req}"`,
            });
          }
        }
        for (const [key, childId] of node.props) {
          if (key in value) {
            validateNode(graph, childId, value[key], joinPath(path, key), depth + 1, maxDepth, stack, sink);
          }
        }
        return;
      }
      case 'array': {
        if (!Array.isArray(value)) {
          sink.add({
            instancePath: path,
            schemaPath: node.schemaPath,
            message: `expected array, got ${describe(value)}`,
          });
          return;
        }
        for (let i = 0; i < value.length; i++) {
          validateNode(graph, node.items, value[i], `${path}/${i}`, depth + 1, maxDepth, stack, sink);
        }
        return;
      }
      case 'union': {
        let matches = 0;
        const causes: ValidationError[] = [];
        for (const option of node.options) {
          // Branch isolation: each oneOf branch gets a fresh copy of the
          // visit-state. Branches may share graph nodes, but never validation
          // state — a node visited in one branch stays unvisited in the next.
          const branchStack = new Set(stack);
          const branchSink = new Collector(Number.MAX_SAFE_INTEGER);
          validateNode(graph, option, value, path, depth, maxDepth, branchStack, branchSink);
          if (branchSink.incomplete) {
            sink.flagIncomplete(branchSink.incompleteReason ?? 'branch incomplete');
          }
          if (!branchSink.incomplete && branchSink.errors.length === 0) {
            matches++;
          } else if (causes.length < 8) {
            causes.push(...branchSink.errors.slice(0, 4));
          }
        }
        if (matches === 1) return;
        sink.add({
          instancePath: path,
          schemaPath: node.schemaPath,
          message:
            matches === 0
              ? `no oneOf branch matched (${node.options.length} branches tried)`
              : `matched ${matches} oneOf branches, expected exactly 1`,
          causes,
        });
        return;
      }
    }
  } finally {
    stack.delete(guard);
  }
}

/** Validates a whole instance against the compiled graph. */
export function validate(graph: Graph, value: unknown, opts: ValidateOptions = {}): ValidationResult {
  const sink = new Collector(opts.maxErrors ?? DEFAULT_MAX_ERRORS);
  validateNode(graph, graph.root, value, '', 0, opts.maxDepth ?? DEFAULT_MAX_DEPTH, new Set(), sink);
  return toResult(sink);
}

/** Re-validates a subtree rooted at a known graph node; used by incremental updates. */
export function collectFrom(
  graph: Graph,
  nodeId: number,
  value: unknown,
  path: string,
  depth: number,
  opts: ValidateOptions = {},
): { errors: ValidationError[]; incomplete: boolean; incompleteReason?: string } {
  const sink = new Collector(opts.maxErrors ?? DEFAULT_MAX_ERRORS);
  validateNode(graph, nodeId, value, path, depth, opts.maxDepth ?? DEFAULT_MAX_DEPTH, new Set(), sink);
  return { errors: sink.errors, incomplete: sink.incomplete, incompleteReason: sink.incompleteReason };
}

function toResult(sink: Sink): ValidationResult {
  // Budget exhaustion is reported as 'incomplete' — never as a schema failure.
  const verdict: Verdict = sink.incomplete ? 'incomplete' : sink.errors.length > 0 ? 'invalid' : 'valid';
  return {
    verdict,
    errors: sink.errors,
    truncated: sink.stop,
    incompleteReason: sink.incompleteReason,
  };
}
