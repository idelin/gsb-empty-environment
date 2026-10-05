import type { PathSeg } from "./path.ts";
import { formatPath } from "./path.ts";

export class QuerySyntaxError extends Error {
  readonly pos: number;
  readonly query: string;
  constructor(message: string, pos: number, query: string) {
    super(`${message} at position ${pos} in ${JSON.stringify(query)}`);
    this.name = "QuerySyntaxError";
    this.pos = pos;
    this.query = query;
  }
}

export type BudgetKind = "parse" | "expand" | "compare";

export class BudgetExceededError extends Error {
  readonly kind: BudgetKind;
  readonly limit: number;
  constructor(kind: BudgetKind, limit: number) {
    super(`query budget exhausted: ${kind} (limit ${limit})`);
    this.name = "BudgetExceededError";
    this.kind = kind;
    this.limit = limit;
  }
}

export class DocumentEditError extends Error {
  readonly path: PathSeg[];
  constructor(message: string, path: PathSeg[]) {
    super(`${message} at ${formatPath(path)}`);
    this.name = "DocumentEditError";
    this.path = [...path];
  }
}

export class CancelledError extends Error {
  constructor() {
    super("operation cancelled");
    this.name = "CancelledError";
  }
}
