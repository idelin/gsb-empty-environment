import {
  aggregate,
  allCompare,
  and,
  anyCompare,
  caseWhen,
  col,
  distinct,
  eqNullSafe,
  existsSubquery,
  filter,
  gt,
  inSubquery,
  isNotNull,
  join,
  leftJoin,
  limit,
  literal,
  lte,
  markJoin,
  notExistsSubquery,
  notInSubquery,
  project,
  rowNumber,
  scalar,
  singleJoin,
  union,
} from "./ast.js";
import type {
  AggregateCall,
  AggregateNode,
  ColumnRef,
  ContextLike,
  Expr,
  ExecutionContext,
  GroupKey,
  MarkSemantics,
  Plan,
  ProjectColumn,
  QueryResult,
  Row,
  SubqueryExpr,
} from "./types.js";
import {
  QueryExecutionError,
  assertFullyDecorrelated,
  evaluatePlan,
  normalizeContext,
  schemaOf,
  walkExpr,
  walkPlan,
} from "./eval.js";

export class RewriteError extends QueryExecutionError {
  constructor(message: string) {
    super(message);
    this.name = "RewriteError";
  }
}

function expressionsOf(plan: Plan): Expr[] {
  switch (plan.kind) {
    case "table":
    case "values":
      return [];
    case "filter":
      return [plan.predicate];
    case "project":
      return plan.columns.map((column) => column.expr);
    case "join":
    case "singleJoin":
      return [plan.condition];
    case "markJoin":
      return [
        plan.condition,
        ...(plan.semantics.left === undefined ? [] : [plan.semantics.left]),
        ...(plan.semantics.right === undefined ? [] : [plan.semantics.right]),
      ];
    case "aggregate":
      return [
        ...plan.groupBy.map((key) => key.expr),
        ...plan.aggregates.flatMap((aggregateCall) =>
          aggregateCall.arg === undefined ? [] : [aggregateCall.arg],
        ),
      ];
    case "union":
      return [];
    case "distinct":
    case "limit":
      return plan.kind === "limit" && plan.orderBy !== undefined
        ? plan.orderBy.map((key) => key.expr)
        : [];
    case "rowNumber":
      return [
        ...plan.partitionBy,
        ...plan.orderBy.map((key) => key.expr),
      ];
    default: {
      const neverPlan: never = plan;
      throw new RewriteError(`Unknown plan node ${String(neverPlan)}`);
    }
  }
}

function exprHasSubquery(expr: Expr): boolean {
  let found = false;
  walkExpr(expr, (node) => {
    if (node.kind === "subquery") found = true;
  });
  return found;
}

function collectAliases(plan: Plan): Set<string> {
  const aliases = new Set<string>();
  walkPlan(plan, (node) => {
    if (node.kind === "table") aliases.add(node.alias ?? node.name);
  });
  return aliases;
}

function unqualifiedName(qualified: string): string {
  const dot = qualified.indexOf(".");
  return dot < 0 ? qualified : qualified.slice(dot + 1);
}

function collectLocalNames(
  plan: Plan,
  context: ExecutionContext,
): Set<string> {
  const names = new Set<string>();
  walkPlan(plan, (node) => {
    switch (node.kind) {
      case "table": {
        let columns = node.columns;
        if (columns === undefined) {
          try {
            columns = schemaOf(node, context).map(unqualifiedName);
          } catch {
            columns = [];
          }
        }
        for (const column of columns) names.add(column);
        return;
      }
      case "values":
        for (const column of node.columns) names.add(column);
        return;
      case "project":
        for (const column of node.columns) names.add(column.as);
        return;
      case "markJoin":
        names.add(node.marker);
        return;
      case "aggregate":
        for (const key of node.groupBy) names.add(key.as);
        for (const aggregateCall of node.aggregates) names.add(aggregateCall.as);
        return;
      case "rowNumber":
        names.add(node.as);
        return;
      default:
        return;
    }
  });
  return names;
}

function collectRefs(plan: Plan): ColumnRef[] {
  const refs: ColumnRef[] = [];
  const seen = new Set<string>();
  walkPlan(plan, (node) => {
    for (const expr of expressionsOf(node)) {
      walkExpr(expr, (part) => {
        if (part.kind !== "column") return;
        const key = `${part.relation ?? ""}\u0000${part.name}`;
        if (seen.has(key)) return;
        seen.add(key);
        refs.push(part);
      });
    }
  });
  return refs;
}

export function freeColumns(
  plan: Plan,
  contextLike?: ContextLike,
): ColumnRef[] {
  const context = normalizeContext(contextLike);
  const aliases = collectAliases(plan);
  const localNames = collectLocalNames(plan, context);
  return collectRefs(plan).filter((ref) => {
    if (ref.relation !== undefined) return !aliases.has(ref.relation);
    return !localNames.has(ref.name);
  });
}

function resolveOuterKey(
  ref: ColumnRef,
  outerSchema: readonly string[],
): string {
  if (ref.relation !== undefined) {
    const key = `${ref.relation}.${ref.name}`;
    if (outerSchema.includes(key)) return key;
    if (outerSchema.includes(ref.name)) return ref.name;
    throw new RewriteError(
      `Correlated column ${key} is not exposed by the outer plan`,
    );
  }
  if (outerSchema.includes(ref.name)) return ref.name;
  const suffix = `.${ref.name}`;
  const matches = outerSchema.filter((key) => key.endsWith(suffix));
  if (matches.length === 1) return matches[0];
  if (matches.length > 1) {
    throw new RewriteError(
      `Correlated column ${ref.name} is ambiguous in the outer plan: ${matches.join(", ")}`,
    );
  }
  throw new RewriteError(
    `Correlated column ${ref.name} is not exposed by the outer plan`,
  );
}

function pairedKeyCondition(
  leftKeys: readonly string[],
  rightKeys: readonly string[],
): Expr {
  return and(
    ...leftKeys.map((key, index) =>
      eqNullSafe(col(key), col(rightKeys[index])),
    ),
  );
}

function rootProjection(columns: readonly string[]): ProjectColumn[] {
  return columns.map((name) => ({ as: name, expr: col(name) }));
}

function guardedAggregate(
  call: AggregateCall,
  presence: string,
): AggregateCall {
  const present = isNotNull(col(presence));
  const value = call.arg ?? literal(1);
  return {
    ...call,
    arg: caseWhen([{ when: present, then: value }], literal(null)),
  };
}

function semanticsFor(expr: SubqueryExpr, right: Expr): MarkSemantics {
  if (expr.mode === "exists") {
    return { quantifier: "exists", negated: expr.negated };
  }
  return {
    quantifier: expr.mode === "all" ? "all" : "any",
    operator: expr.operator ?? "=",
    left: expr.left,
    right,
    negated: expr.negated,
  };
}

export class GeneralUnnestingRewriter {
  private readonly context: ExecutionContext;
  private counter = 0;

  constructor(contextLike?: ContextLike) {
    this.context = normalizeContext(contextLike);
  }

  private fresh(prefix: string, used: readonly string[] = []): string {
    let name: string;
    do {
      name = `${prefix}_${this.counter++}`;
    } while (used.includes(name));
    return name;
  }

  rewrite(plan: Plan): Plan {
    const originalSchema = schemaOf(plan, this.context);
    let rewritten = this.rewritePlan(plan);
    const finalSchema = schemaOf(rewritten, this.context);
    const needsProjection =
      finalSchema.length !== originalSchema.length ||
      finalSchema.some((column) => !originalSchema.includes(column));
    if (needsProjection) {
      for (const column of originalSchema) {
        if (!finalSchema.includes(column)) {
          throw new RewriteError(
            `Unnesting lost output column ${column} from the root plan`,
          );
        }
      }
      rewritten = project(rootProjection(originalSchema), rewritten);
    }
    assertFullyDecorrelated(rewritten);
    return rewritten;
  }

  private rewritePlan(plan: Plan): Plan {
    const originalSchema = schemaOf(plan, this.context);
    const rewritten = this.rewritePlanNode(plan);
    const finalSchema = schemaOf(rewritten, this.context);
    const needsProjection =
      finalSchema.length !== originalSchema.length ||
      finalSchema.some((column) => !originalSchema.includes(column));
    if (!needsProjection) return rewritten;
    for (const column of originalSchema) {
      if (!finalSchema.includes(column)) {
        throw new RewriteError(
          `Unnesting lost output column ${column} from a query block`,
        );
      }
    }
    return project(rootProjection(originalSchema), rewritten);
  }

  private rewritePlanNode(plan: Plan): Plan {
    switch (plan.kind) {
      case "table":
      case "values":
        return plan;
      case "filter": {
        const input = this.rewritePlan(plan.input);
        const predicate = this.rewriteExpr(plan.predicate, input);
        return filter(predicate.expr, predicate.input);
      }
      case "project": {
        let input = this.rewritePlan(plan.input);
        const columns: ProjectColumn[] = [];
        for (const column of plan.columns) {
          const rewritten = this.rewriteExpr(column.expr, input);
          input = rewritten.input;
          columns.push({ as: column.as, expr: rewritten.expr });
        }
        return project(columns, input);
      }
      case "join": {
        const left = this.rewritePlan(plan.left);
        const right = this.rewritePlan(plan.right);
        if (!exprHasSubquery(plan.condition)) {
          return { ...plan, left, right };
        }
        if (plan.joinType !== "inner") {
          throw new RewriteError(
            "Subqueries in non-inner join conditions are not supported by this normalizer",
          );
        }
        const cross = join(left, right, literal(true), "inner");
        const predicate = this.rewriteExpr(plan.condition, cross);
        const filtered = filter(predicate.expr, predicate.input);
        const output = schemaOf(plan, this.context);
        return project(rootProjection(output), filtered);
      }
      case "markJoin": {
        const left = this.rewritePlan(plan.left);
        const right = this.rewritePlan(plan.right);
        return { ...plan, left, right };
      }
      case "singleJoin": {
        const left = this.rewritePlan(plan.left);
        const right = this.rewritePlan(plan.right);
        return { ...plan, left, right };
      }
      case "aggregate": {
        let input = this.rewritePlan(plan.input);
        const groupBy: GroupKey[] = [];
        for (const key of plan.groupBy) {
          const rewritten = this.rewriteExpr(key.expr, input);
          input = rewritten.input;
          groupBy.push({ as: key.as, expr: rewritten.expr });
        }
        const aggregates: AggregateCall[] = [];
        for (const aggregateCall of plan.aggregates) {
          if (aggregateCall.arg === undefined) {
            aggregates.push(aggregateCall);
          } else {
            const rewritten = this.rewriteExpr(aggregateCall.arg, input);
            input = rewritten.input;
            aggregates.push({ ...aggregateCall, arg: rewritten.expr });
          }
        }
        return { ...plan, input, groupBy, aggregates };
      }
      case "union":
        return {
          ...plan,
          left: this.rewritePlan(plan.left),
          right: this.rewritePlan(plan.right),
        };
      case "distinct":
        return { ...plan, input: this.rewritePlan(plan.input) };
      case "limit": {
        let input = this.rewritePlan(plan.input);
        let orderBy = plan.orderBy;
        if (orderBy !== undefined) {
          const rewrittenKeys = [];
          for (const key of orderBy) {
            const rewritten = this.rewriteExpr(key.expr, input);
            input = rewritten.input;
            rewrittenKeys.push({ ...key, expr: rewritten.expr });
          }
          orderBy = rewrittenKeys;
        }
        return { ...plan, input, orderBy };
      }
      case "rowNumber":
        return { ...plan, input: this.rewritePlan(plan.input) };
      default: {
        const neverPlan: never = plan;
        throw new RewriteError(`Unknown plan node ${String(neverPlan)}`);
      }
    }
  }

  private rewriteExpr(
    expr: Expr,
    input: Plan,
  ): { expr: Expr; input: Plan } {
    switch (expr.kind) {
      case "column":
      case "literal":
        return { expr, input };
      case "binary": {
        const left = this.rewriteExpr(expr.left, input);
        const right = this.rewriteExpr(expr.right, left.input);
        return {
          expr: { ...expr, left: left.expr, right: right.expr },
          input: right.input,
        };
      }
      case "unary": {
        const arg = this.rewriteExpr(expr.arg, input);
        return { expr: { ...expr, arg: arg.expr }, input: arg.input };
      }
      case "case": {
        let current = input;
        const whens = [];
        for (const branch of expr.whens) {
          const when = this.rewriteExpr(branch.when, current);
          const then = this.rewriteExpr(branch.then, when.input);
          current = then.input;
          whens.push({ when: when.expr, then: then.expr });
        }
        const otherwise =
          expr.otherwise === undefined
            ? undefined
            : this.rewriteExpr(expr.otherwise, current);
        return {
          expr: {
            ...expr,
            whens,
            ...(otherwise === undefined ? {} : { otherwise: otherwise.expr }),
          },
          input: otherwise === undefined ? current : otherwise.input,
        };
      }
      case "coalesce": {
        let current = input;
        const args: Expr[] = [];
        for (const arg of expr.args) {
          const rewritten = this.rewriteExpr(arg, current);
          current = rewritten.input;
          args.push(rewritten.expr);
        }
        return { expr: { ...expr, args }, input: current };
      }
      case "call": {
        let current = input;
        const args: Expr[] = [];
        for (const arg of expr.args) {
          const rewritten = this.rewriteExpr(arg, current);
          current = rewritten.input;
          args.push(rewritten.expr);
        }
        return { expr: { ...expr, args }, input: current };
      }
      case "subquery": {
        let current = input;
        let left = expr.left;
        if (left !== undefined) {
          const rewrittenLeft = this.rewriteExpr(left, current);
          current = rewrittenLeft.input;
          left = rewrittenLeft.expr;
        }
        return this.applySubquery(
          left === expr.left ? expr : { ...expr, left },
          current,
        );
      }
      default: {
        const neverExpr: never = expr;
        throw new RewriteError(`Unknown expression ${String(neverExpr)}`);
      }
    }
  }

  private applySubquery(
    expr: SubqueryExpr,
    outer: Plan,
  ): { expr: Expr; input: Plan } {
    const free = freeColumns(expr.query, this.context);
    const outerSchema = schemaOf(outer, this.context);
    const keys = Array.from(
      new Set(free.map((ref) => resolveOuterKey(ref, outerSchema))),
    );

    let domain: Plan | undefined;
    let rawRight: Plan;
    if (keys.length > 0) {
      domain = this.buildDomain(outer, keys);
      rawRight = this.decorrelatePlan(expr.query, domain, keys);
    } else {
      rawRight = expr.query;
    }
    let right = this.rewritePlan(rawRight);
    const rightSchema = schemaOf(right, this.context);
    const originalSchema = schemaOf(expr.query, this.context);
    const target = originalSchema[0] ?? rightSchema.find((key) => !keys.includes(key));

    const projectedColumns: ProjectColumn[] = [];
    const rightKeys = keys.map((key) =>
      this.fresh("__gsb_domain_key", rightSchema),
    );
    for (const [index, key] of keys.entries()) {
      if (rightSchema.includes(key)) {
        projectedColumns.push({ as: rightKeys[index], expr: col(key) });
      }
    }
    const condition =
      keys.length === 0
        ? literal(true)
        : pairedKeyCondition(keys, rightKeys);

    if (expr.mode === "scalar") {
      if (target === undefined) {
        throw new RewriteError("Scalar subquery has no output column");
      }
      const hidden = this.fresh("__gsb_scalar", rightSchema);
      projectedColumns.push({ as: hidden, expr: col(target) });
      const projected = project(projectedColumns, right);
      const joined = singleJoin(outer, projected, condition);
      return { expr: col(hidden), input: joined };
    }

    if (expr.mode === "exists") {
      const projected =
        projectedColumns.length === 0
          ? project([], right)
          : project(projectedColumns, right);
      const marker = this.fresh("__gsb_mark", schemaOf(outer, this.context));
      const joined = markJoin(
        outer,
        projected,
        condition,
        marker,
        semanticsFor(expr, literal(false)),
      );
      return { expr: col(marker), input: joined };
    }

    if (target === undefined) {
      throw new RewriteError(
        `${expr.mode.toUpperCase()} subquery has no output column`,
      );
    }
    const hidden = this.fresh("__gsb_value", rightSchema);
    projectedColumns.push({ as: hidden, expr: col(target) });
    const projected = project(projectedColumns, right);
    const marker = this.fresh("__gsb_mark", schemaOf(outer, this.context));
    const joined = markJoin(
      outer,
      projected,
      condition,
      marker,
      semanticsFor(expr, col(hidden)),
    );
    return { expr: col(marker), input: joined };
  }

  private buildDomain(outer: Plan, keys: readonly string[]): Plan {
    return distinct(
      project(
        keys.map((key) => ({ as: key, expr: col(key) })),
        outer,
      ),
    );
  }

  private decorrelatePlan(
    plan: Plan,
    domain: Plan,
    keys: readonly string[],
  ): Plan {
    switch (plan.kind) {
      case "table":
      case "values":
        return join(domain, plan, literal(true), "inner");
      case "filter":
        return filter(
          plan.predicate,
          this.decorrelatePlan(plan.input, domain, keys),
        );
      case "project": {
        const input = this.decorrelatePlan(plan.input, domain, keys);
        const inputSchema = schemaOf(input, this.context);
        const present = new Set(plan.columns.map((column) => column.as));
        const columns = plan.columns.slice();
        for (const key of keys) {
          if (!present.has(key) && inputSchema.includes(key)) {
            columns.push({ as: key, expr: col(key) });
          }
        }
        return project(columns, input);
      }
      case "join": {
        const left = this.decorrelatePlan(plan.left, domain, keys);
        const decoratedRight = this.decorrelatePlan(plan.right, domain, keys);
        const rightSchema = schemaOf(decoratedRight, this.context);
        const rightKeys = keys.map((key) =>
          this.fresh("__gsb_domain_key", rightSchema),
        );
        const right = this.renameKeys(decoratedRight, keys, rightKeys);
        const joined: Plan = {
          ...plan,
          left,
          right,
          condition: and(
            plan.condition,
            keys.length === 0
              ? literal(true)
              : pairedKeyCondition(keys, rightKeys),
          ),
        };
        return this.foldNamedKeys(joined, keys, rightKeys);
      }
      case "markJoin": {
        const left = this.decorrelatePlan(plan.left, domain, keys);
        const decoratedRight = this.decorrelatePlan(plan.right, domain, keys);
        const rightSchema = schemaOf(decoratedRight, this.context);
        const rightKeys = keys.map((key) =>
          this.fresh("__gsb_domain_key", rightSchema),
        );
        const right = this.renameKeys(decoratedRight, keys, rightKeys);
        return {
          ...plan,
          left,
          right,
          condition: and(
            plan.condition,
            keys.length === 0
              ? literal(true)
              : pairedKeyCondition(keys, rightKeys),
          ),
        };
      }
      case "singleJoin": {
        const left = this.decorrelatePlan(plan.left, domain, keys);
        const decoratedRight = this.decorrelatePlan(plan.right, domain, keys);
        const rightSchema = schemaOf(decoratedRight, this.context);
        const rightKeys = keys.map((key) =>
          this.fresh("__gsb_domain_key", rightSchema),
        );
        const right = this.renameKeys(decoratedRight, keys, rightKeys);
        const joined: Plan = {
          ...plan,
          left,
          right,
          condition: and(
            plan.condition,
            keys.length === 0
              ? literal(true)
              : pairedKeyCondition(keys, rightKeys),
          ),
        };
        return this.foldNamedKeys(joined, keys, rightKeys);
      }
      case "aggregate":
        return this.decorrelateAggregate(plan, domain, keys);
      case "union":
        return {
          ...plan,
          left: this.decorrelatePlan(plan.left, domain, keys),
          right: this.decorrelatePlan(plan.right, domain, keys),
        };
      case "distinct":
        return {
          ...plan,
          input: this.decorrelatePlan(plan.input, domain, keys),
        };
      case "limit":
        return this.decorrelateLimit(plan, domain, keys);
      case "rowNumber": {
        const input = this.decorrelatePlan(plan.input, domain, keys);
        return {
          ...plan,
          input,
          partitionBy: [
            ...keys.map((key) => col(key)),
            ...plan.partitionBy,
          ],
        };
      }
      default: {
        const neverPlan: never = plan;
        throw new RewriteError(`Unknown plan node ${String(neverPlan)}`);
      }
    }
  }

  private decorrelateAggregate(
    plan: AggregateNode,
    domain: Plan,
    keys: readonly string[],
  ): Plan {
    const input = this.decorrelatePlan(plan.input, domain, keys);
    const domainGroups: GroupKey[] = keys.map((key) => ({
      as: key,
      expr: col(key),
    }));
    if (plan.groupBy.length > 0 || keys.length === 0) {
      return aggregate(
        input,
        [...domainGroups, ...plan.groupBy],
        plan.aggregates,
      );
    }

    const inputSchema = schemaOf(input, this.context);
    const presence = this.fresh("__gsb_present", inputSchema);
    const rightBase = project(
      [
        ...inputSchema.map((key) => ({ as: key, expr: col(key) })),
        { as: presence, expr: literal(1) },
      ],
      input,
    );
    const rightKeys = keys.map((key) =>
      this.fresh("__gsb_domain_key", inputSchema),
    );
    const right = this.renameKeys(rightBase, keys, rightKeys);
    const source = leftJoin(
      domain,
      right,
      pairedKeyCondition(keys, rightKeys),
    );
    return aggregate(
      source,
      domainGroups,
      plan.aggregates.map((call) => guardedAggregate(call, presence)),
    );
  }

  private decorrelateLimit(
    plan: Extract<Plan, { kind: "limit" }>,
    domain: Plan,
    keys: readonly string[],
  ): Plan {
    const input = this.decorrelatePlan(plan.input, domain, keys);
    const inputSchema = schemaOf(input, this.context);
    const rowNumberColumn = this.fresh("__gsb_row_number", inputSchema);
    const ranked = rowNumber(
      input,
      keys.map((key) => col(key)),
      plan.orderBy ?? [],
      rowNumberColumn,
    );
    const offset = plan.offset ?? 0;
    const filtered = filter(
      and(
        gt(col(rowNumberColumn), literal(offset)),
        lte(col(rowNumberColumn), literal(offset + plan.count)),
      ),
      ranked,
    );
    return project(rootProjection(inputSchema), filtered);
  }

  private renameKeys(
    plan: Plan,
    keys: readonly string[],
    renamed: readonly string[],
  ): Plan {
    const schema = schemaOf(plan, this.context);
    const mapping = new Map(keys.map((key, index) => [key, renamed[index]]));
    return project(
      schema.map((column) => ({
        as: mapping.get(column) ?? column,
        expr: col(column),
      })),
      plan,
    );
  }

  private foldNamedKeys(
    plan: Plan,
    keys: readonly string[],
    renamed: readonly string[],
  ): Plan {
    const schema = schemaOf(plan, this.context);
    const hidden = new Set(renamed);
    return project(
      schema
        .filter((column) => !hidden.has(column))
        .map((column) => {
          const index = keys.indexOf(column);
          return {
            as: column,
            expr:
              index < 0
                ? col(column)
                : {
                    kind: "coalesce" as const,
                    args: [col(column), col(renamed[index])],
                  },
          };
        }),
      plan,
    );
  }
}

export function rewriteQuery(
  plan: Plan,
  contextLike?: ContextLike,
): Plan {
  return new GeneralUnnestingRewriter(contextLike).rewrite(plan);
}

export const unnest = rewriteQuery;
export const generalUnnest = rewriteQuery;
export const rewrite = rewriteQuery;

export function executeUnnested(
  plan: Plan,
  contextLike?: ContextLike,
) {
  return evaluatePlan(rewriteQuery(plan, contextLike), contextLike);
}

export const executeRewritten = executeUnnested;
export const runUnnested = executeUnnested;
export const runOptimized = executeUnnested;

export class UnnestingExecutor {
  constructor(private readonly contextLike?: ContextLike) {}

  plan(plan: Plan): Plan {
    return rewriteQuery(plan, this.contextLike);
  }

  execute(plan: Plan) {
    return evaluatePlan(this.plan(plan), this.contextLike);
  }
}

export function rowsOf(result: { rows: readonly Row[] }): Row[] {
  return result.rows.slice();
}

export interface ExecutionComparison {
  readonly reference: QueryResult;
  readonly rewritten: QueryResult;
  readonly rewrittenPlan: Plan;
  readonly equal: boolean;
  readonly firstMismatch?: number;
}

function canonical(value: unknown): string {
  if (value === null || typeof value !== "object") {
    if (typeof value === "bigint") return `bigint:${value.toString()}`;
    return `${typeof value}:${String(value)}`;
  }
  if (Array.isArray(value)) {
    return `[${value.map(canonical).join(",")}]`;
  }
  const record = value as Record<string, unknown>;
  return `{${Object.keys(record)
    .sort()
    .map((key) => `${key}:${canonical(record[key])}`)
    .join(",")}}`;
}

function rowsEqual(
  left: readonly Row[],
  right: readonly Row[],
  columns: readonly string[],
): { equal: boolean; firstMismatch?: number } {
  if (left.length !== right.length) return { equal: false };
  for (let index = 0; index < left.length; index += 1) {
    const leftKey = canonical(columns.map((column) => left[index][column]));
    const rightKey = canonical(columns.map((column) => right[index][column]));
    if (leftKey !== rightKey) return { equal: false, firstMismatch: index };
  }
  return { equal: true };
}

export function compareExecution(
  plan: Plan,
  contextLike?: ContextLike,
): ExecutionComparison {
  const reference = evaluatePlan(plan, contextLike);
  const rewrittenPlan = rewriteQuery(plan, contextLike);
  const rewritten = evaluatePlan(rewrittenPlan, contextLike);
  const columnsEqual =
    canonical(reference.columns) === canonical(rewritten.columns);
  const rows = rowsEqual(reference.rows, rewritten.rows, reference.columns);
  return {
    reference,
    rewritten,
    rewrittenPlan,
    equal: columnsEqual && rows.equal,
    ...(rows.firstMismatch === undefined
      ? {}
      : { firstMismatch: rows.firstMismatch }),
  };
}

export const compareNestedAndUnnested = compareExecution;

export function assertExecutionEquivalent(
  plan: Plan,
  contextLike?: ContextLike,
): ExecutionComparison {
  const comparison = compareExecution(plan, contextLike);
  if (!comparison.equal) {
    const index =
      comparison.firstMismatch === undefined
        ? "column metadata"
        : `row ${comparison.firstMismatch}`;
    throw new QueryExecutionError(
      `Nested reference execution and unnesting disagree at ${index}`,
    );
  }
  return comparison;
}

export const assertEquivalentUnnesting = assertExecutionEquivalent;
