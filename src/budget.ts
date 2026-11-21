import {
  parseGraphQL,
  type ArgumentNode,
  type DefinitionNode,
  type DirectiveNode,
  type DocumentNode,
  type FieldNode,
  type FragmentDefinitionNode,
  type OperationDefinitionNode,
  type SelectionSetNode,
  type ValueNode,
} from "./graphql.ts";

export type BudgetViolationCode =
  | "BUDGET_EXCEEDED"
  | "DEPTH_EXCEEDED"
  | "PAGE_SIZE_EXCEEDED"
  | "INVALID_PAGE_SIZE"
  | "UNRESOLVED_VARIABLE"
  | "INVALID_DIRECTIVE"
  | "UNKNOWN_FRAGMENT"
  | "FRAGMENT_CYCLE"
  | "CURSOR_FALLBACK"
  | "COST_OVERFLOW";

export interface BudgetViolation {
  code: BudgetViolationCode;
  message: string;
  path: string[];
  pathString: string;
  line?: number;
  column?: number;
  limit?: number;
  actual?: number;
  totalCost?: number;
  depth?: number;
  multiplier?: number;
  variable?: string;
}

export interface CostNode {
  path: string;
  pathSegments: string[];
  field: string;
  responseName: string;
  depth: number;
  multiplier: number;
  cost: number;
  cumulativeCost: number;
  pageSize: number | null;
  line: number;
  column: number;
}

export interface CursorBudgetInfo {
  independent: true;
  fallbackRejected: boolean;
  ignoredArguments: string[];
  paginationMode: "declared" | "cursor";
}

export interface BudgetResult {
  ok: boolean;
  allowed: boolean;
  rejected: boolean;
  maxCost: number;
  budget: number;
  cost: number;
  totalCost: number;
  maxDepth: number;
  maxDepthObserved: number;
  operationName: string | null;
  path: string | null;
  pathSegments: string[];
  pathString: string | null;
  exceededPath: string | null;
  overBudgetPath: string | null;
  reason: string | null;
  violations: BudgetViolation[];
  breakdown: CostNode[];
  costs: CostNode[];
  fieldCount: number;
  fragmentExpansions: string[];
  fragmentsUsed: string[];
  fragmentExpansionCount: number;
  cursor: CursorBudgetInfo;
}

export interface BudgetOptions {
  maxCost?: number;
  budget?: number;
  maxDepth?: number;
  maxPageSize?: number;
  defaultPageSize?: number;
  fieldWeight?: number;
  depthWeight?: number;
  pageSizeArguments?: readonly string[];
  cursorArguments?: readonly string[];
  listFields?: readonly string[];
  variables?: Record<string, unknown>;
  operationName?: string;
  cursor?: unknown;
  cursorFallback?: boolean | number | { requests?: number; pages?: number };
  paginationMode?: "declared" | "cursor" | "cursor-fallback" | "single-request";
  requests?: number;
}

export interface BudgetRequest {
  query: string | DocumentNode;
  variables?: Record<string, unknown>;
  operationName?: string;
  maxCost?: number;
  budget?: number;
}

export type BudgetInput = string | DocumentNode | BudgetRequest;

interface NormalizedOptions {
  maxCost: number;
  maxDepth: number | null;
  maxPageSize: number | null;
  defaultPageSize: number;
  fieldWeight: number;
  depthWeight: number;
  pageSizeArguments: Set<string>;
  cursorArguments: Set<string>;
  listFields: Set<string>;
  variables: Record<string, unknown>;
  operationName: string | null;
  paginationMode: "declared" | "cursor";
  cursorFallback: boolean;
}

interface WalkContext {
  pathSegments: string[];
  depth: number;
  multiplier: number;
  seenFragments: Set<string>;
  activeFragments: ReadonlySet<string>;
}

interface ResolvedValue {
  resolved: boolean;
  value?: unknown;
  variable?: string;
}

const DEFAULT_PAGE_SIZE_ARGUMENTS = [
  "first",
  "last",
  "limit",
  "take",
  "pageSize",
  "perPage",
] as const;

const DEFAULT_CURSOR_ARGUMENTS = [
  "after",
  "before",
  "cursor",
  "endCursor",
  "startCursor",
  "pageToken",
] as const;

export class BudgetConfigurationError extends TypeError {
  constructor(message: string) {
    super(message);
    this.name = "BudgetConfigurationError";
  }
}

export class BudgetRejectedError extends Error {
  readonly result: BudgetResult;
  readonly code: BudgetViolationCode;
  readonly path: string | null;
  readonly pathSegments: string[];

  constructor(result: BudgetResult) {
    const violation = result.violations[0];
    const suffix = violation?.pathString ? ` at ${violation.pathString}` : "";
    super(`${violation?.message ?? "Query rejected"}${suffix}`);
    this.name = "BudgetRejectedError";
    this.result = result;
    this.code = violation?.code ?? "BUDGET_EXCEEDED";
    this.path = result.path;
    this.pathSegments = result.pathSegments;
  }
}

export { BudgetRejectedError as BudgetExceededError };

class BudgetEvaluator {
  private readonly options: NormalizedOptions;
  private readonly document: DocumentNode;
  private readonly fragments = new Map<string, FragmentDefinitionNode>();
  private readonly operation: OperationDefinitionNode;
  private readonly variables = new Map<string, unknown>();
  private readonly violations: BudgetViolation[] = [];
  private readonly violationKeys = new Set<string>();
  private readonly costNodes: CostNode[] = [];
  private readonly fragmentExpansions: string[] = [];
  private readonly cursorArgumentsSeen = new Set<string>();
  private totalCost = 0;
  private maxDepthObserved = 0;
  private budgetExceeded = false;
  private depthExceeded = false;
  private pageSizeExceeded = false;
  private costOverflowed = false;

  constructor(
    input: BudgetInput,
    options: BudgetOptions,
  ) {
    const request = normalizeInput(input, options);
    this.document =
      typeof request.query === "string"
        ? parseGraphQL(request.query)
        : request.query;
    this.options = normalizeOptions(request.options);

    const operations: OperationDefinitionNode[] = [];
    for (const definition of this.document.definitions) {
      if (definition.kind === "FragmentDefinition") {
        if (this.fragments.has(definition.name)) {
          throw new BudgetConfigurationError(
            `Duplicate fragment definition '${definition.name}'`,
          );
        }
        this.fragments.set(definition.name, definition);
      } else {
        operations.push(definition);
      }
    }

    if (operations.length === 0) {
      throw new BudgetConfigurationError(
        "Query document does not contain an operation",
      );
    }

    const operationName = this.options.operationName;
    if (operationName === null) {
      if (operations.length > 1) {
        throw new BudgetConfigurationError(
          "operationName is required when a document contains multiple operations",
        );
      }
      this.operation = operations[0] as OperationDefinitionNode;
    } else {
      const operation = operations.find(
        (candidate) => candidate.name === operationName,
      );
      if (operation === undefined) {
        throw new BudgetConfigurationError(
          `Operation '${operationName}' was not found`,
        );
      }
      this.operation = operation;
    }

    this.initializeVariables();
    this.rejectCursorFallbackIfRequested();
  }

  evaluate(): BudgetResult {
    const operation = this.operation;
    const operationPath: string[] = [];

    if (
      this.directivesInclude(
        operation.directives,
        operationPath,
        operation.loc.line,
        operation.loc.column,
      )
    ) {
      this.walkSelectionSet(operation.selectionSet, {
        pathSegments: operationPath,
        depth: 0,
        multiplier: 1,
        seenFragments: new Set<string>(),
        activeFragments: new Set<string>(),
      });
    }

    const firstViolation = this.violations[0] ?? null;
    const budgetViolation = this.violations.find(
      (violation) => violation.code === "BUDGET_EXCEEDED",
    );
    const rejected = this.violations.length > 0;
    const pathSegments = firstViolation?.path ?? [];
    const path = pathSegments.length > 0 ? pathSegments.join(".") : null;
    const fragmentNames = this.fragmentExpansions.filter(
      (name, index, all) => all.indexOf(name) === index,
    );

    return {
      ok: !rejected,
      allowed: !rejected,
      rejected,
      maxCost: this.options.maxCost,
      budget: this.options.maxCost,
      cost: this.totalCost,
      totalCost: this.totalCost,
      maxDepth: this.maxDepthObserved,
      maxDepthObserved: this.maxDepthObserved,
      operationName: operation.name,
      path,
      pathSegments: [...pathSegments],
      pathString: path,
      exceededPath: path,
      overBudgetPath: budgetViolation?.pathString ?? null,
      reason: firstViolation?.message ?? null,
      violations: this.violations.map((violation) => ({
        ...violation,
        path: [...violation.path],
      })),
      breakdown: this.costNodes.map((node) => ({
        ...node,
        pathSegments: [...node.pathSegments],
      })),
      costs: this.costNodes.map((node) => ({
        ...node,
        pathSegments: [...node.pathSegments],
      })),
      fieldCount: this.costNodes.length,
      fragmentExpansions: [...this.fragmentExpansions],
      fragmentsUsed: fragmentNames,
      fragmentExpansionCount: this.fragmentExpansions.length,
      cursor: {
        independent: true,
        fallbackRejected: this.violations.some(
          (violation) => violation.code === "CURSOR_FALLBACK",
        ),
        ignoredArguments: [...this.cursorArgumentsSeen],
        paginationMode: this.options.paginationMode,
      },
    };
  }

  private initializeVariables(): void {
    for (const definition of this.operation.variableDefinitions) {
      if (Object.prototype.hasOwnProperty.call(this.options.variables, definition.name)) {
        this.variables.set(definition.name, this.options.variables[definition.name]);
        continue;
      }

      if (definition.defaultValue !== null) {
        const resolved = resolveLiteral(definition.defaultValue);
        if (resolved.resolved) {
          this.variables.set(definition.name, resolved.value);
        }
      }
    }
  }

  private rejectCursorFallbackIfRequested(): void {
    if (this.options.paginationMode === "cursor" || this.options.cursorFallback) {
      this.addViolation({
        code: "CURSOR_FALLBACK",
        message:
          "Cursor pagination cannot be used to spread a query's declared cost across requests",
        path: [],
        pathString: "",
      });
    }
  }

  private walkSelectionSet(
    selectionSet: SelectionSetNode,
    context: WalkContext,
  ): void {
    for (const selection of selectionSet.selections) {
      if (selection.kind === "Field") {
        this.walkField(selection, context);
        continue;
      }

      if (selection.kind === "InlineFragment") {
        if (
          !this.directivesInclude(
            selection.directives,
            context.pathSegments,
            selection.loc.line,
            selection.loc.column,
          )
        ) {
          continue;
        }
        this.walkSelectionSet(selection.selectionSet, {
          ...context,
          seenFragments: context.seenFragments,
        });
        continue;
      }

      this.walkFragmentSpread(selection.name, selection.directives, context);
    }
  }

  private walkField(field: FieldNode, context: WalkContext): void {
    const responseName = field.alias ?? field.name;
    const pathSegments = [...context.pathSegments, responseName];
    const pathString = pathSegments.join(".");

    if (
      !this.directivesInclude(
        field.directives,
        pathSegments,
        field.loc.line,
        field.loc.column,
      )
    ) {
      return;
    }

    const depth = context.depth + 1;
    this.maxDepthObserved = Math.max(this.maxDepthObserved, depth);

    if (
      this.options.maxDepth !== null &&
      depth > this.options.maxDepth &&
      !this.depthExceeded
    ) {
      this.depthExceeded = true;
      this.addViolation({
        code: "DEPTH_EXCEEDED",
        message: `Field depth ${depth} exceeds the limit ${this.options.maxDepth}`,
        path: pathSegments,
        pathString,
        line: field.loc.line,
        column: field.loc.column,
        limit: this.options.maxDepth,
        actual: depth,
        depth,
        multiplier: context.multiplier,
      });
    }

    const page = this.resolvePage(field, pathSegments, pathString);
    const rawCost =
      this.options.fieldWeight *
      Math.pow(depth, this.options.depthWeight) *
      context.multiplier;
    const cost = safeCost(rawCost);
    if (!Number.isFinite(rawCost) && !this.costOverflowed) {
      this.costOverflowed = true;
      this.addViolation({
        code: "COST_OVERFLOW",
        message: "Static query cost exceeded the numeric range",
        path: pathSegments,
        pathString,
        line: field.loc.line,
        column: field.loc.column,
        depth,
        multiplier: context.multiplier,
      });
    }

    this.totalCost = safeAdd(this.totalCost, cost);
    this.costNodes.push({
      path: pathString,
      pathSegments,
      field: field.name,
      responseName,
      depth,
      multiplier: context.multiplier,
      cost,
      cumulativeCost: this.totalCost,
      pageSize: page.pageSize,
      line: field.loc.line,
      column: field.loc.column,
    });

    if (
      !this.budgetExceeded &&
      Number.isFinite(this.options.maxCost) &&
      this.totalCost > this.options.maxCost
    ) {
      this.budgetExceeded = true;
      this.addViolation({
        code: "BUDGET_EXCEEDED",
        message: `Query cost ${formatNumber(this.totalCost)} exceeds budget ${formatNumber(this.options.maxCost)}`,
        path: pathSegments,
        pathString,
        line: field.loc.line,
        column: field.loc.column,
        limit: this.options.maxCost,
        actual: this.totalCost,
        totalCost: this.totalCost,
        depth,
        multiplier: context.multiplier,
      });
    }

    if (field.selectionSet !== null) {
      this.walkSelectionSet(field.selectionSet, {
        pathSegments,
        depth,
        multiplier: safeMultiply(context.multiplier, page.pageSize),
        seenFragments: new Set<string>(),
        activeFragments: context.activeFragments,
      });
    }
  }

  private walkFragmentSpread(
    name: string,
    directives: DirectiveNode[],
    context: WalkContext,
  ): void {
    const pathString = context.pathSegments.join(".");
    if (
      !this.directivesInclude(
        directives,
        context.pathSegments,
        undefined,
        undefined,
      )
    ) {
      return;
    }

    const fragment = this.fragments.get(name);
    if (fragment === undefined) {
      this.addViolation({
        code: "UNKNOWN_FRAGMENT",
        message: `Unknown fragment '${name}'`,
        path: context.pathSegments,
        pathString,
      });
      return;
    }

    if (
      !this.directivesInclude(
        fragment.directives,
        context.pathSegments,
        fragment.loc.line,
        fragment.loc.column,
      )
    ) {
      return;
    }

    if (context.activeFragments.has(name)) {
      this.addViolation({
        code: "FRAGMENT_CYCLE",
        message: `Fragment cycle detected through '${name}'`,
        path: context.pathSegments,
        pathString,
        line: fragment.loc.line,
        column: fragment.loc.column,
      });
      return;
    }

    // A fragment spread contributes one merged field group per selection set.
    // Do not charge the same fragment twice when it is reached through
    // repeated spreads or through two different intermediate fragments.
    if (context.seenFragments.has(name)) {
      return;
    }
    context.seenFragments.add(name);

    const activeFragments = new Set(context.activeFragments);
    activeFragments.add(name);
    this.fragmentExpansions.push(name);
    this.walkSelectionSet(fragment.selectionSet, {
      ...context,
      activeFragments,
    });
  }

  private directivesInclude(
    directives: readonly DirectiveNode[],
    pathSegments: string[],
    line: number | undefined,
    column: number | undefined,
  ): boolean {
    let included = true;
    for (const directive of directives) {
      if (directive.name !== "skip" && directive.name !== "include") {
        continue;
      }

      const condition = directive.arguments.find(
        (argument) => argument.name === "if",
      );
      if (condition === undefined) {
        this.addViolation({
          code: "INVALID_DIRECTIVE",
          message: `@${directive.name} requires an 'if' argument`,
          path: pathSegments,
          pathString: pathSegments.join("."),
          line: line ?? directive.loc.line,
          column: column ?? directive.loc.column,
        });
        continue;
      }

      const resolved = this.resolveValue(condition.value);
      if (!resolved.resolved) {
        this.addUnresolvedVariableViolation(
          resolved.variable ?? "<unknown>",
          pathSegments,
          line ?? condition.loc.line,
          column ?? condition.loc.column,
          `@${directive.name}`,
        );
        continue;
      }

      if (typeof resolved.value !== "boolean") {
        this.addViolation({
          code: "INVALID_DIRECTIVE",
          message: `@${directive.name}(if:) must resolve to a boolean`,
          path: pathSegments,
          pathString: pathSegments.join("."),
          line: line ?? condition.loc.line,
          column: column ?? condition.loc.column,
        });
        continue;
      }

      if (directive.name === "skip" && resolved.value) {
        included = false;
      }
      if (directive.name === "include" && !resolved.value) {
        included = false;
      }
    }
    return included;
  }

  private resolvePage(
    field: FieldNode,
    pathSegments: string[],
    pathString: string,
  ): { pageSize: number } {
    const pageArguments: ArgumentNode[] = [];
    for (const argument of field.arguments) {
      if (this.options.cursorArguments.has(argument.name)) {
        this.cursorArgumentsSeen.add(argument.name);
      }
      if (this.options.pageSizeArguments.has(argument.name)) {
        pageArguments.push(argument);
      }
    }

    if (pageArguments.length === 0) {
      const pageSize = this.options.listFields.has(field.name)
        ? this.options.defaultPageSize
        : 1;
      return { pageSize };
    }

    const sizes: number[] = [];
    for (const argument of pageArguments) {
      const resolved = this.resolveValue(argument.value);
      if (!resolved.resolved) {
        this.addUnresolvedVariableViolation(
          resolved.variable ?? "<unknown>",
          pathSegments,
          argument.loc.line,
          argument.loc.column,
          `@${field.name}(${argument.name}:)`,
        );
        sizes.push(Math.max(1, this.options.defaultPageSize));
        continue;
      }

      if (resolved.value === null || resolved.value === undefined) {
        this.addViolation({
          code: "INVALID_PAGE_SIZE",
          message: `Pagination argument '${argument.name}' cannot be null`,
          path: pathSegments,
          pathString,
          line: argument.loc.line,
          column: argument.loc.column,
        });
        sizes.push(Math.max(1, this.options.defaultPageSize));
        continue;
      }

      if (
        typeof resolved.value !== "number" ||
        !Number.isFinite(resolved.value) ||
        !Number.isInteger(resolved.value) ||
        resolved.value < 0
      ) {
        this.addViolation({
          code: "INVALID_PAGE_SIZE",
          message: `Pagination argument '${argument.name}' must resolve to a non-negative integer`,
          path: pathSegments,
          pathString,
          line: argument.loc.line,
          column: argument.loc.column,
          actual:
            typeof resolved.value === "number" ? resolved.value : undefined,
        });
        sizes.push(Math.max(1, this.options.defaultPageSize));
        continue;
      }
      sizes.push(resolved.value);
    }

    // A query should not declare both first and last, but if it does, taking
    // the larger value is the conservative estimate.
    const pageSize = sizes.length === 0 ? 1 : Math.max(...sizes);
    if (
      this.options.maxPageSize !== null &&
      pageSize > this.options.maxPageSize &&
      !this.pageSizeExceeded
    ) {
      this.pageSizeExceeded = true;
      this.addViolation({
        code: "PAGE_SIZE_EXCEEDED",
        message: `Page size ${pageSize} exceeds the limit ${this.options.maxPageSize}`,
        path: pathSegments,
        pathString,
        limit: this.options.maxPageSize,
        actual: pageSize,
        line: field.loc.line,
        column: field.loc.column,
      });
    }

    return { pageSize };
  }

  private resolveValue(value: ValueNode): ResolvedValue {
    if (value.kind === "Variable") {
      if (!this.variables.has(value.name)) {
        return { resolved: false, variable: value.name };
      }
      const runtime = this.variables.get(value.name);
      if (runtime === undefined) {
        return { resolved: false, variable: value.name };
      }
      return { resolved: true, value: runtime };
    }
    return resolveLiteral(value);
  }

  private addUnresolvedVariableViolation(
    variable: string,
    pathSegments: string[],
    line: number,
    column: number,
    usage: string,
  ): void {
    this.addViolation({
      code: "UNRESOLVED_VARIABLE",
      message: `Variable '$${variable}' used by ${usage} has no value or default`,
      path: pathSegments,
      pathString: pathSegments.join("."),
      line,
      column,
      variable,
    });
  }

  private addViolation(violation: BudgetViolation): void {
    const key = [
      violation.code,
      violation.path.join("."),
      violation.variable ?? "",
      violation.actual ?? "",
      violation.limit ?? "",
    ].join("|");
    if (this.violationKeys.has(key)) {
      return;
    }
    this.violationKeys.add(key);
    this.violations.push(violation);
  }
}

function normalizeInput(
  input: BudgetInput,
  options: BudgetOptions,
): { query: string | DocumentNode; options: BudgetOptions } {
  if (typeof input === "string") {
    return { query: input, options };
  }
  if ("kind" in input && input.kind === "Document") {
    return { query: input, options };
  }
  if (typeof input === "object" && input !== null && "query" in input) {
    return {
      query: input.query,
      options: {
        ...options,
        variables: {
          ...(options.variables ?? {}),
          ...(input.variables ?? {}),
        },
        operationName: input.operationName ?? options.operationName,
        maxCost: options.maxCost ?? input.maxCost,
        budget: options.budget ?? input.budget,
      },
    };
  }
  throw new BudgetConfigurationError(
    "Budget input must be a query string, GraphQL document, or request object",
  );
}

function normalizeOptions(options: BudgetOptions): NormalizedOptions {
  const maxCost = options.maxCost ?? options.budget ?? Number.POSITIVE_INFINITY;
  assertFiniteLimit(maxCost, "maxCost");

  const maxDepth = options.maxDepth ?? null;
  if (
    maxDepth !== null &&
    (!Number.isInteger(maxDepth) || maxDepth < 0 || !Number.isFinite(maxDepth))
  ) {
    throw new BudgetConfigurationError(
      "maxDepth must be a non-negative integer",
    );
  }

  const maxPageSize = options.maxPageSize ?? null;
  if (
    maxPageSize !== null &&
    (!Number.isInteger(maxPageSize) ||
      maxPageSize < 0 ||
      !Number.isFinite(maxPageSize))
  ) {
    throw new BudgetConfigurationError(
      "maxPageSize must be a non-negative integer",
    );
  }

  const defaultPageSize = options.defaultPageSize ?? 1;
  if (
    !Number.isInteger(defaultPageSize) ||
    defaultPageSize < 0 ||
    !Number.isFinite(defaultPageSize)
  ) {
    throw new BudgetConfigurationError(
      "defaultPageSize must be a non-negative integer",
    );
  }

  const fieldWeight = options.fieldWeight ?? 1;
  assertFiniteLimit(fieldWeight, "fieldWeight");

  const depthWeight = options.depthWeight ?? 1;
  assertFiniteLimit(depthWeight, "depthWeight");

  if (
    options.variables !== undefined &&
    (options.variables === null || typeof options.variables !== "object")
  ) {
    throw new BudgetConfigurationError("variables must be an object");
  }

  const cursorFallback = isCursorFallbackAttempt(options);
  return {
    maxCost,
    maxDepth,
    maxPageSize,
    defaultPageSize,
    fieldWeight,
    depthWeight,
    pageSizeArguments: new Set(
      options.pageSizeArguments ?? DEFAULT_PAGE_SIZE_ARGUMENTS,
    ),
    cursorArguments: new Set(
      options.cursorArguments ?? DEFAULT_CURSOR_ARGUMENTS,
    ),
    listFields: new Set(options.listFields ?? []),
    variables: options.variables ?? {},
    operationName: options.operationName ?? null,
    paginationMode:
      options.paginationMode === "cursor" ||
      options.paginationMode === "cursor-fallback"
        ? "cursor"
        : "declared",
    cursorFallback,
  };
}

function isCursorFallbackAttempt(options: BudgetOptions): boolean {
  if (
    options.paginationMode === "cursor" ||
    options.paginationMode === "cursor-fallback"
  ) {
    return true;
  }
  if (options.requests !== undefined && options.requests > 1) {
    return true;
  }

  const fallback = options.cursorFallback;
  if (fallback === true) {
    return true;
  }
  if (typeof fallback === "number") {
    return fallback > 1;
  }
  if (typeof fallback === "object" && fallback !== null) {
    return (fallback.requests ?? 1) > 1 || (fallback.pages ?? 1) > 1;
  }
  return false;
}

function assertFiniteLimit(value: number, name: string): void {
  if (
    typeof value !== "number" ||
    Number.isNaN(value) ||
    value < 0 ||
    value === Number.NEGATIVE_INFINITY
  ) {
    throw new BudgetConfigurationError(
      `${name} must be a non-negative number`,
    );
  }
}

function resolveLiteral(value: ValueNode): ResolvedValue {
  switch (value.kind) {
    case "IntValue":
    case "FloatValue":
    case "StringValue":
    case "BooleanValue":
    case "EnumValue":
      return { resolved: true, value: value.value };
    case "NullValue":
      return { resolved: true, value: null };
    case "ListValue": {
      const values: unknown[] = [];
      for (const item of value.values) {
        const resolved = resolveLiteral(item);
        if (!resolved.resolved) {
          return resolved;
        }
        values.push(resolved.value);
      }
      return { resolved: true, value: values };
    }
    case "ObjectValue": {
      const object: Record<string, unknown> = {};
      for (const field of value.fields) {
        const resolved = resolveLiteral(field.value);
        if (!resolved.resolved) {
          return resolved;
        }
        object[field.name] = resolved.value;
      }
      return { resolved: true, value: object };
    }
    case "Variable":
      return { resolved: false, variable: value.name };
  }
}

function safeCost(value: number): number {
  if (!Number.isFinite(value)) {
    return Number.MAX_SAFE_INTEGER;
  }
  return Math.min(Number.MAX_SAFE_INTEGER, value);
}

function safeAdd(left: number, right: number): number {
  const value = left + right;
  if (!Number.isFinite(value)) {
    return Number.MAX_SAFE_INTEGER;
  }
  return Math.min(Number.MAX_SAFE_INTEGER, value);
}

function safeMultiply(left: number, right: number): number {
  const value = left * right;
  if (!Number.isFinite(value)) {
    return Number.MAX_SAFE_INTEGER;
  }
  return Math.min(Number.MAX_SAFE_INTEGER, value);
}

function formatNumber(value: number): string {
  return Number.isInteger(value) ? String(value) : value.toFixed(2);
}

export function estimateBudget(
  input: BudgetInput,
  options: BudgetOptions = {},
): BudgetResult {
  return new BudgetEvaluator(input, options).evaluate();
}

export const estimateCost = estimateBudget;
export const estimateQueryCost = estimateBudget;
export const evaluateBudget = estimateBudget;
export const checkBudget = estimateBudget;

export function assertWithinBudget(
  input: BudgetInput,
  options: BudgetOptions = {},
): BudgetResult {
  const result = estimateBudget(input, options);
  if (!result.ok) {
    throw new BudgetRejectedError(result);
  }
  return result;
}

export const enforceBudget = assertWithinBudget;

export class StaticBudget {
  readonly options: BudgetOptions;

  constructor(options: BudgetOptions = {}) {
    this.options = { ...options };
  }

  estimate(
    input: BudgetInput,
    overrides: BudgetOptions = {},
  ): BudgetResult {
    return estimateBudget(input, { ...this.options, ...overrides });
  }

  check(input: BudgetInput, overrides: BudgetOptions = {}): BudgetResult {
    return this.estimate(input, overrides);
  }

  assert(input: BudgetInput, overrides: BudgetOptions = {}): BudgetResult {
    const result = this.estimate(input, overrides);
    if (!result.ok) {
      throw new BudgetRejectedError(result);
    }
    return result;
  }
}

export function createStaticBudget(
  options: BudgetOptions = {},
): StaticBudget {
  return new StaticBudget(options);
}

export {
  StaticBudget as QueryBudget,
  StaticBudget as BudgetEstimator,
};

export const createBudget = createStaticBudget;
export const assertBudget = assertWithinBudget;
export const calculateQueryCost = estimateBudget;
