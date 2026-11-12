/**
 * Row-level-security (RLS) predicate ordering.
 *
 * A security barrier may only move a user predicate ahead of the policy gate
 * when every function used by that predicate is leakproof. Otherwise the
 * predicate must run after the complete RLS gate, including all permissive and
 * restrictive policies.
 */

export type PredicateKind = "policy" | "user";
export type PolicyMode = "permissive" | "restrictive";

export const POLICY_PREDICATE = "policy" as const;
export const USER_PREDICATE = "user" as const;
export const PERMISSIVE_POLICY = "permissive" as const;
export const RESTRICTIVE_POLICY = "restrictive" as const;

export const DEFAULT_DENY_PREDICATE_ID = "__rls_default_deny__";

/**
 * PostgreSQL's comparison operators are leakproof built-ins. Function names
 * are included for callers that model operators as functions.
 */
export const LEAKPROOF_BUILTIN_FUNCTIONS: ReadonlySet<string> = new Set([
  "=",
  "==",
  "<>",
  "!=",
  "<",
  "<=",
  ">",
  ">=",
  "is distinct from",
  "is not distinct from",
  "is null",
  "is not null",
  "eq",
  "ne",
  "lt",
  "lte",
  "gt",
  "gte",
]);

export interface FunctionInfo {
  name?: string;
  leakproof?: boolean;
  isLeakproof?: boolean;
  isLeakProof?: boolean;
  leakProof?: boolean;
  builtin?: boolean;
  [key: string]: unknown;
}

/**
 * Broad input shape for predicates. `kind`, `policyMode` and `leakproof` are
 * the canonical fields; the aliases make the package usable with the smaller
 * metadata objects normally produced by SQL optimizers.
 */
export interface PredicateInput {
  id?: string | number;
  name?: string;
  predicateId?: string | number;
  label?: string;
  kind?: string;
  type?: string;
  predicateType?: string;
  category?: string;
  isPolicy?: boolean;
  isUser?: boolean;
  policyMode?: string;
  policyType?: string;
  policyKind?: string;
  policy?: string;
  rlsMode?: string;
  mode?: string;
  permissive?: boolean | string;
  restrictive?: boolean;
  isPermissive?: boolean;
  isRestrictive?: boolean;
  expression?: string;
  expr?: string;
  sql?: string;
  condition?: string;
  filter?: string;
  predicate?: string;
  leakproof?: boolean;
  isLeakproof?: boolean;
  isLeakProof?: boolean;
  leakProof?: boolean;
  functionLeakproof?: boolean;
  function?: FunctionInfo | string | boolean;
  fn?: FunctionInfo | string | boolean;
  func?: FunctionInfo | string | boolean;
  functions?: readonly (FunctionInfo | string | boolean)[];
  functionNames?: readonly string[];
  operator?: string;
  builtin?: boolean;
  defaultDeny?: boolean;
  synthetic?: boolean;
  [key: string]: unknown;
}

export interface SqlPredicate extends PredicateInput {
  id: string;
  kind: PredicateKind;
  policyMode?: PolicyMode;
}

export interface PolicyPredicate extends SqlPredicate {
  kind: "policy";
  policyMode: PolicyMode;
}

export interface UserPredicate extends SqlPredicate {
  kind: "user";
}

export type PolicyAst =
  | {
      readonly type: "predicate";
      readonly id: string;
      readonly expression: string;
    }
  | {
      readonly type: "or";
      readonly children: readonly PolicyAst[];
    }
  | {
      readonly type: "and";
      readonly children: readonly PolicyAst[];
    }
  | {
      readonly type: "false";
    };

export interface PolicyCombination {
  /** All supplied policy predicates, in input order. */
  readonly policies: readonly PredicateInput[];
  readonly permissive: readonly PredicateInput[];
  readonly permissivePolicies: readonly PredicateInput[];
  readonly restrictive: readonly PredicateInput[];
  readonly restrictivePolicies: readonly PredicateInput[];
  /** `(p1 OR p2) AND r1 AND r2`, or the literal `false`. */
  readonly expression: string;
  readonly combinedExpression: string;
  readonly sql: string;
  readonly ast: PolicyAst;
  readonly defaultDeny: boolean;
  readonly isDefaultDeny: boolean;
  /** Policies in evaluation order: permissive group, then restrictives. */
  readonly policyOrder: readonly PredicateInput[];
}

export interface OrderOptions {
  readonly functionRegistry?: Readonly<
    Record<string, boolean | FunctionInfo>
  >;
  readonly defaultDenyPredicateId?: string;
  /**
   * Include the synthesized `false` gate when no PERMISSIVE policy exists.
   * Enabled by default so the returned order is directly executable.
   */
  readonly includeDefaultDenyPredicate?: boolean;
}

export interface PredicatePlanInput {
  /** Mixed policy and user predicates. */
  readonly predicates?: readonly PredicateInput[];
  readonly policies?: readonly PredicateInput[];
  readonly policyPredicates?: readonly PredicateInput[];
  readonly users?: readonly PredicateInput[];
  readonly userPredicates?: readonly PredicateInput[];
  /**
   * Optional existing plan to audit. An unsafe proposed order is rejected
   * before a safe order is returned.
   */
  readonly proposedOrder?: readonly PredicateInput[];
  readonly requestedOrder?: readonly PredicateInput[];
}

export interface PredicateOrderPlan {
  readonly order: OrderedPredicates;
  readonly executionOrder: OrderedPredicates;
  readonly predicates: OrderedPredicates;
  readonly ordered: OrderedPredicates;
  readonly orderedPredicates: OrderedPredicates;
  readonly sequence: readonly string[];
  readonly policy: PolicyCombination;
  readonly policyGate: PolicyCombination;
  readonly policyExpression: string;
  readonly defaultDeny: boolean;
  readonly safe: true;
  readonly securityBarrier: true;
}

/**
 * An array that also carries the complete plan as non-enumerable properties.
 * This keeps `orderPredicates(...).map(...)` conventional while allowing
 * `.order`, `.policyExpression` and `.defaultDeny` to be inspected directly.
 */
export interface OrderedPredicates extends Array<PredicateInput> {
  readonly order: OrderedPredicates;
  readonly executionOrder: OrderedPredicates;
  readonly predicates: OrderedPredicates;
  readonly ordered: OrderedPredicates;
  readonly orderedPredicates: OrderedPredicates;
  readonly sequence: readonly string[];
  readonly policy: PolicyCombination;
  readonly policyGate: PolicyCombination;
  readonly policyExpression: string;
  readonly defaultDeny: boolean;
  readonly safe: true;
  readonly securityBarrier: true;
  readonly plan: PredicateOrderPlan;
}

export interface UnsafeOrderViolation {
  readonly type:
    | "unsafe-user-before-policy"
    | "missing-policy-gate"
    | "missing-default-deny";
  readonly predicateId: string;
  readonly message: string;
}

export interface PredicateOrderValidation {
  readonly valid: boolean;
  readonly violations: readonly UnsafeOrderViolation[];
  readonly defaultDeny: boolean;
  readonly permissiveCount: number;
  readonly restrictiveCount: number;
}

export class UnsafePredicateOrderError extends Error {
  readonly code = "UNSAFE_PREDICATE_ORDER";
  readonly violations: readonly UnsafeOrderViolation[];
  readonly validation: PredicateOrderValidation;

  constructor(validation: PredicateOrderValidation) {
    const details = validation.violations
      .map((violation) => violation.message)
      .join("; ");
    super(`Unsafe row-level-security predicate order: ${details}`);
    this.name = "UnsafePredicateOrderError";
    this.validation = validation;
    this.violations = validation.violations;
  }
}

export class PredicateInputError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "PredicateInputError";
  }
}

interface NormalizedPredicate {
  readonly input: PredicateInput;
  readonly id: string;
  readonly kind: PredicateKind;
  readonly mode?: PolicyMode;
  readonly leakproof: boolean;
  readonly expression: string | null;
  readonly index: number;
}

interface GatheredInput {
  readonly policies: readonly PredicateInput[];
  readonly users: readonly PredicateInput[];
  readonly mixed: readonly PredicateInput[];
  readonly options: OrderOptions;
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function firstBoolean(...values: readonly unknown[]): boolean | undefined {
  for (const value of values) {
    if (typeof value === "boolean") {
      return value;
    }
  }
  return undefined;
}

function normalizeName(value: unknown): string {
  return typeof value === "string"
    ? value.trim().toLowerCase().replace(/[_-]+/g, " ")
    : "";
}

function kindFromPredicate(
  predicate: PredicateInput,
  fallback: PredicateKind | undefined,
): PredicateKind {
  const marker = normalizeName(
    predicate.kind ??
      predicate.type ??
      predicate.predicateType ??
      predicate.category,
  );

  if (
    marker === "policy" ||
    marker === "rls" ||
    marker === "rls policy" ||
    marker === "security" ||
    marker === "security policy" ||
    marker === "permissive" ||
    marker === "restrictive"
  ) {
    return "policy";
  }
  if (
    marker === "user" ||
    marker === "user predicate" ||
    marker === "query" ||
    marker === "filter"
  ) {
    return "user";
  }
  if (predicate.isPolicy === true) return "policy";
  if (predicate.isUser === true) return "user";

  const policyMode = normalizeName(
    predicate.policyMode ??
      predicate.policyType ??
      predicate.policyKind ??
      predicate.policy ??
      predicate.rlsMode ??
      predicate.mode,
  );
  if (
    policyMode === "permissive" ||
    policyMode === "restrictive" ||
    typeof predicate.permissive === "boolean" ||
    typeof predicate.isPermissive === "boolean" ||
    typeof predicate.isRestrictive === "boolean" ||
    predicate.permissive === "permissive" ||
    predicate.permissive === "restrictive" ||
    predicate.restrictive === true
  ) {
    return "policy";
  }

  return fallback ?? "user";
}

function policyModeFromPredicate(
  predicate: PredicateInput,
  index: number,
): PolicyMode {
  const typeMarker = normalizeName(predicate.type);
  const kindMarker = normalizeName(predicate.kind);
  const markerMode =
    typeMarker === "permissive" || typeMarker === "restrictive"
      ? typeMarker
      : kindMarker === "permissive" || kindMarker === "restrictive"
        ? kindMarker
        : "";
  const raw = normalizeName(
    predicate.policyMode ??
      predicate.policyType ??
      predicate.policyKind ??
      predicate.policy ??
      predicate.rlsMode ??
      predicate.mode ??
      predicate.permissive ??
      markerMode,
  );

  if (raw === "permissive" || raw === "perm" || raw === "allow") {
    return "permissive";
  }
  if (raw === "restrictive" || raw === "restrict" || raw === "deny") {
    return "restrictive";
  }
  if (predicate.permissive === true || predicate.isPermissive === true) {
    return "permissive";
  }
  if (
    predicate.permissive === false ||
    predicate.restrictive === true ||
    predicate.isRestrictive === true
  ) {
    return "restrictive";
  }
  if (raw !== "") {
    throw new PredicateInputError(
      `Policy predicate ${describePredicateId(predicate, index)} has an invalid policy mode.`,
    );
  }

  return "permissive";
}

function describePredicateId(
  predicate: PredicateInput,
  index: number,
): string {
  const raw =
    predicate.id ??
    predicate.predicateId ??
    predicate.name ??
    predicate.label;
  return raw === undefined ? `#${index}` : String(raw);
}

function predicateId(predicate: PredicateInput, index: number): string {
  return describePredicateId(predicate, index);
}

function predicateExpression(
  predicate: PredicateInput,
  _index: number,
): string | null {
  const expression =
    predicate.expression ??
    predicate.expr ??
    predicate.sql ??
    predicate.condition ??
    predicate.filter ??
    predicate.predicate;
  if (typeof expression === "string" && expression.trim() !== "") {
    return expression.trim();
  }
  return null;
}

function registryFunction(
  name: string,
  registry: Readonly<Record<string, boolean | FunctionInfo>> | undefined,
): boolean | FunctionInfo | undefined {
  if (!registry) return undefined;
  const exact = registry[name];
  if (exact !== undefined) return exact;
  const normalized = name.trim().toLowerCase();
  for (const [key, value] of Object.entries(registry)) {
    if (key.trim().toLowerCase() === normalized) {
      return value;
    }
  }
  return undefined;
}

/**
 * Returns true only when the function is explicitly leakproof or is a known
 * built-in comparison. Unknown functions are deliberately treated as leaky.
 */
export function isLeakproofFunction(
  value: FunctionInfo | string | boolean | null | undefined,
  registry?: Readonly<Record<string, boolean | FunctionInfo>>,
): boolean {
  if (typeof value === "boolean") return value;
  if (value === null || value === undefined) return false;
  if (typeof value === "string") {
    const registered = registryFunction(value, registry);
    if (registered !== undefined) {
      return isLeakproofFunction(registered, registry);
    }
    return LEAKPROOF_BUILTIN_FUNCTIONS.has(value.trim().toLowerCase());
  }

  const explicit = firstBoolean(
    value.leakproof,
    value.isLeakproof,
    value.isLeakProof,
    value.leakProof,
  );
  if (explicit !== undefined) return explicit;
  if (value.builtin === false) return false;
  if (value.builtin === true) return true;

  const name = typeof value.name === "string" ? value.name : "";
  if (name !== "") {
    const registered = registryFunction(name, registry);
    if (registered !== undefined) {
      return isLeakproofFunction(registered, registry);
    }
    if (LEAKPROOF_BUILTIN_FUNCTIONS.has(name.trim().toLowerCase())) {
      return true;
    }
  }

  return false;
}

function expressionHasFunctionCall(expression: string): boolean {
  return /[A-Za-z_][\w.]*\s*\(/.test(expression);
}

function expressionIsBuiltinComparison(expression: string): boolean {
  if (expressionHasFunctionCall(expression)) return false;
  if (
    /\b(select|from|where|exists|case|when|then|else|end)\b/i.test(
      expression,
    )
  ) {
    return false;
  }
  return /(?:=|==|<>|!=|<=|>=|<|>|\bIS\b|\bBETWEEN\b|\bIN\b)/i.test(
    expression,
  );
}

function predicateLeakproof(
  predicate: PredicateInput,
  expression: string | null,
  registry: Readonly<Record<string, boolean | FunctionInfo>> | undefined,
): boolean {
  const explicit = firstBoolean(
    predicate.leakproof,
    predicate.isLeakproof,
    predicate.isLeakProof,
    predicate.leakProof,
    predicate.functionLeakproof,
  );
  if (explicit !== undefined) return explicit;

  const single = predicate.function ?? predicate.fn ?? predicate.func;
  if (single !== undefined) {
    return isLeakproofFunction(single, registry);
  }

  const functions = predicate.functions;
  if (Array.isArray(functions) && functions.length > 0) {
    return functions.every((fn) => isLeakproofFunction(fn, registry));
  }

  const functionNames = predicate.functionNames;
  if (Array.isArray(functionNames) && functionNames.length > 0) {
    return functionNames.every((name) =>
      isLeakproofFunction(name, registry),
    );
  }

  if (predicate.builtin === true) return true;

  const operator =
    typeof predicate.operator === "string" ? predicate.operator : "";
  if (
    operator !== "" &&
    expressionIsBuiltinComparison(operator) &&
    (expression === null || !expressionHasFunctionCall(expression))
  ) {
    return true;
  }

  if (expression !== null) {
    return expressionIsBuiltinComparison(expression);
  }

  return false;
}

function normalizePredicate(
  input: PredicateInput,
  defaultKind: PredicateKind | undefined,
  index: number,
  options: OrderOptions,
): NormalizedPredicate {
  if (!isRecord(input)) {
    throw new PredicateInputError(
      `Predicate at index ${index} is not an object.`,
    );
  }
  const kind = kindFromPredicate(input, defaultKind);
  const mode =
    kind === "policy" ? policyModeFromPredicate(input, index) : undefined;
  const expression = predicateExpression(input, index);
  return {
    input,
    id: predicateId(input, index),
    kind,
    mode,
    leakproof: predicateLeakproof(
      input,
      expression,
      options.functionRegistry,
    ),
    expression,
    index,
  };
}

function astForPredicate(predicate: NormalizedPredicate): PolicyAst {
  return {
    type: "predicate",
    id: predicate.id,
    expression: predicate.expression ?? predicate.id,
  };
}

function hasTopLevelBooleanOperator(expression: string): boolean {
  let depth = 0;
  let quote = "";
  for (let index = 0; index < expression.length; index += 1) {
    const char = expression[index];
    if (quote !== "") {
      if (char === quote) {
        if (expression[index + 1] === quote) {
          index += 1;
        } else {
          quote = "";
        }
      }
      continue;
    }
    if (char === "'" || char === '"') {
      quote = char;
      continue;
    }
    if (char === "(") {
      depth += 1;
      continue;
    }
    if (char === ")") {
      depth = Math.max(0, depth - 1);
      continue;
    }
    if (depth !== 0 || !/[A-Za-z_]/.test(char)) {
      continue;
    }
    let end = index + 1;
    while (end < expression.length && /[\w$]/.test(expression[end])) {
      end += 1;
    }
    const word = expression.slice(index, end).toUpperCase();
    if (word === "AND" || word === "OR") {
      return true;
    }
    index = end - 1;
  }
  return false;
}

function policyChildSql(ast: PolicyAst): string {
  if (ast.type !== "predicate") {
    return astToSql(ast);
  }
  return hasTopLevelBooleanOperator(ast.expression)
    ? `(${ast.expression})`
    : ast.expression;
}

function astToSql(ast: PolicyAst): string {
  switch (ast.type) {
    case "predicate":
      return ast.expression;
    case "or":
      return `(${ast.children
        .map(policyChildSql)
        .join(" OR ")})`;
    case "and":
      return `(${ast.children
        .map(policyChildSql)
        .join(" AND ")})`;
    case "false":
      return "false";
  }
}

function combinePolicyAst(
  permissive: readonly NormalizedPredicate[],
  restrictive: readonly NormalizedPredicate[],
): PolicyAst {
  if (permissive.length === 0) {
    return { type: "false" };
  }

  const permissiveAst: PolicyAst =
    permissive.length === 1
      ? astForPredicate(permissive[0])
      : {
          type: "or",
          children: permissive.map(astForPredicate),
        };

  if (restrictive.length === 0) {
    return permissiveAst;
  }

  return {
    type: "and",
    children: [permissiveAst, ...restrictive.map(astForPredicate)],
  };
}

function normalizePolicyList(
  policies: readonly PredicateInput[],
  options: OrderOptions,
): readonly NormalizedPredicate[] {
  return policies.map((policy, index) =>
    normalizePredicate(policy, "policy", index, options),
  );
}

/**
 * Combines RLS policies using the engine rules:
 *
 *   (permissive_1 OR permissive_2 ...) AND restrictive_1 AND restrictive_2 ...
 *
 * If there is no PERMISSIVE policy, the result is the constant `false`,
 * regardless of any restrictive policies.
 */
export function mergePolicies(
  policies: readonly PredicateInput[],
  options: OrderOptions = {},
): PolicyCombination {
  if (!Array.isArray(policies)) {
    throw new PredicateInputError("Policies must be an array.");
  }
  const normalized = normalizePolicyList(policies, options);
  const permissive = normalized.filter(
    (policy) => policy.mode === "permissive",
  );
  const restrictive = normalized.filter(
    (policy) => policy.mode === "restrictive",
  );
  const ast = combinePolicyAst(permissive, restrictive);
  const expression = astToSql(ast);
  const defaultDeny = permissive.length === 0;

  return {
    policies,
    permissive: permissive.map((policy) => policy.input),
    permissivePolicies: permissive.map((policy) => policy.input),
    restrictive: restrictive.map((policy) => policy.input),
    restrictivePolicies: restrictive.map((policy) => policy.input),
    expression,
    combinedExpression: expression,
    sql: expression,
    ast,
    defaultDeny,
    isDefaultDeny: defaultDeny,
    policyOrder: [...permissive, ...restrictive].map(
      (policy) => policy.input,
    ),
  };
}

export const combinePolicies = mergePolicies;
export const mergePolicyPredicates = mergePolicies;
export const combinePolicyPredicates = mergePolicies;

/**
 * Returns the combined SQL expression only. The literal `false` means the
 * query is denied because no PERMISSIVE policy exists.
 */
export function buildPolicyExpression(
  policies: readonly PredicateInput[],
  options: OrderOptions = {},
): string {
  return mergePolicies(policies, options).expression;
}

export const getPolicyExpression = buildPolicyExpression;

function defaultDenyPredicate(options: OrderOptions): PolicyPredicate {
  return {
    id: options.defaultDenyPredicateId ?? DEFAULT_DENY_PREDICATE_ID,
    kind: "policy",
    policyMode: "permissive",
    expression: "false",
    sql: "false",
    leakproof: true,
    defaultDeny: true,
    synthetic: true,
  };
}

function gatherInput(
  policyInput: readonly PredicateInput[] | PredicatePlanInput,
  userInput?: readonly PredicateInput[] | OrderOptions,
  maybeOptions?: OrderOptions,
): GatheredInput {
  if (Array.isArray(policyInput)) {
    if (Array.isArray(userInput)) {
      return {
        policies: policyInput,
        users: userInput,
        mixed: [],
        options: maybeOptions ?? {},
      };
    }
    return {
      policies: [],
      users: [],
      mixed: policyInput,
      options: isRecord(userInput) ? (userInput as OrderOptions) : {},
    };
  }

  if (!isRecord(policyInput)) {
    throw new PredicateInputError(
      "Expected an array of predicates or a predicate plan input.",
    );
  }

  const input = policyInput as unknown as PredicatePlanInput;
  const hasList =
    Array.isArray(input.predicates) ||
    Array.isArray(input.policies) ||
    Array.isArray(input.policyPredicates) ||
    Array.isArray(input.users) ||
    Array.isArray(input.userPredicates);

  if (!hasList) {
    throw new PredicateInputError(
      "Predicate plan input must contain a predicates, policies, or userPredicates array.",
    );
  }

  return {
    policies: [...(input.policies ?? []), ...(input.policyPredicates ?? [])],
    users: [...(input.users ?? []), ...(input.userPredicates ?? [])],
    mixed: input.predicates ?? [],
    options: isRecord(userInput) ? (userInput as OrderOptions) : {},
  };
}

function collectPredicates(
  gathered: GatheredInput,
  options: OrderOptions,
): {
  readonly policies: readonly NormalizedPredicate[];
  readonly users: readonly NormalizedPredicate[];
} {
  let index = 0;
  const policies: NormalizedPredicate[] = [];
  const users: NormalizedPredicate[] = [];

  for (const policy of gathered.policies) {
    const normalized = normalizePredicate(policy, "policy", index, options);
    if (normalized.kind === "policy") {
      policies.push(normalized);
    } else {
      users.push(normalized);
    }
    index += 1;
  }
  for (const user of gathered.users) {
    const normalized = normalizePredicate(user, "user", index, options);
    if (normalized.kind === "user") {
      users.push(normalized);
    } else {
      policies.push(normalized);
    }
    index += 1;
  }
  for (const predicate of gathered.mixed) {
    const normalized = normalizePredicate(predicate, undefined, index, options);
    if (normalized.kind === "policy") {
      policies.push(normalized);
    } else {
      users.push(normalized);
    }
    index += 1;
  }

  return { policies, users };
}

function decorateOrder(
  predicates: readonly PredicateInput[],
  policy: PolicyCombination,
): OrderedPredicates {
  const order = [...predicates] as OrderedPredicates;
  const plan = {
    order,
    executionOrder: order,
    predicates: order,
    ordered: order,
    orderedPredicates: order,
    sequence: predicates.map((predicate, index) =>
      describePredicateId(predicate, index),
    ),
    policy,
    policyGate: policy,
    policyExpression: policy.expression,
    defaultDeny: policy.defaultDeny,
    safe: true,
    securityBarrier: true,
  } as PredicateOrderPlan;

  Object.defineProperties(order, {
    order: { value: order, enumerable: false },
    executionOrder: { value: order, enumerable: false },
    predicates: { value: order, enumerable: false },
    ordered: { value: order, enumerable: false },
    orderedPredicates: { value: order, enumerable: false },
    sequence: {
      value: predicates.map((predicate, index) =>
        describePredicateId(predicate, index),
      ),
      enumerable: false,
    },
    policy: { value: policy, enumerable: false },
    policyGate: { value: policy, enumerable: false },
    policyExpression: { value: policy.expression, enumerable: false },
    defaultDeny: { value: policy.defaultDeny, enumerable: false },
    safe: { value: true, enumerable: false },
    securityBarrier: { value: true, enumerable: false },
    plan: { value: plan, enumerable: false },
  });

  return order;
}

function buildPlan(
  policyInput: readonly PredicateInput[] | PredicatePlanInput,
  userInput?: readonly PredicateInput[] | OrderOptions,
  maybeOptions?: OrderOptions,
): PredicateOrderPlan {
  const gathered = gatherInput(policyInput, userInput, maybeOptions);
  const options = gathered.options;
  const collected = collectPredicates(gathered, options);
  const policy = mergePolicies(
    collected.policies.map((predicate) => predicate.input),
    options,
  );

  const proposed =
    !Array.isArray(policyInput) &&
    isRecord(policyInput) &&
    (Array.isArray((policyInput as PredicatePlanInput).proposedOrder) ||
      Array.isArray((policyInput as PredicatePlanInput).requestedOrder))
      ? ((policyInput as PredicatePlanInput).proposedOrder ??
        (policyInput as PredicatePlanInput).requestedOrder ??
        [])
      : undefined;
  if (proposed !== undefined) {
    validatePredicateOrder(proposed, options);
  }

  const safeUsers = collected.users.filter((predicate) => predicate.leakproof);
  const unsafeUsers = collected.users.filter(
    (predicate) => !predicate.leakproof,
  );

  let policyExecution: readonly PredicateInput[];
  if (policy.defaultDeny) {
    policyExecution =
      options.includeDefaultDenyPredicate === false
        ? []
        : [defaultDenyPredicate(options)];
  } else {
    policyExecution = policy.policyOrder;
  }

  const execution = [
    ...safeUsers.map((predicate) => predicate.input),
    ...policyExecution,
    ...unsafeUsers.map((predicate) => predicate.input),
  ];
  validatePredicateOrder(execution, options);
  const order = decorateOrder(execution, policy);

  return {
    order,
    executionOrder: order,
    predicates: order,
    ordered: order,
    orderedPredicates: order,
    sequence: execution.map((predicate, index) =>
      describePredicateId(predicate, index),
    ),
    policy,
    policyGate: policy,
    policyExpression: policy.expression,
    defaultDeny: policy.defaultDeny,
    safe: true,
    securityBarrier: true,
  };
}

/**
 * Produces the execution order of a query planning node.
 *
 * Leakproof user predicates are pushed ahead of the policy gate, the policy
 * gate follows, and the remaining user predicates run only after the gate.
 * If no PERMISSIVE policy exists, a synthetic `false` gate is inserted.
 */
export function planPredicateOrder(
  predicates: readonly PredicateInput[],
  options?: OrderOptions,
): PredicateOrderPlan;
export function planPredicateOrder(
  policies: readonly PredicateInput[],
  users: readonly PredicateInput[],
  options?: OrderOptions,
): PredicateOrderPlan;
export function planPredicateOrder(
  input: PredicatePlanInput,
  options?: OrderOptions,
): PredicateOrderPlan;
export function planPredicateOrder(
  policyInput: readonly PredicateInput[] | PredicatePlanInput,
  userInput?: readonly PredicateInput[] | OrderOptions,
  maybeOptions?: OrderOptions,
): PredicateOrderPlan {
  return buildPlan(policyInput, userInput, maybeOptions);
}

export const planPredicates = planPredicateOrder;
export const planSecurityPredicates = planPredicateOrder;

/**
 * Convenience form: returns the ordered predicate array. The array also has
 * non-enumerable `.order`, `.policy`, `.policyExpression`, `.defaultDeny` and
 * `.plan` properties.
 */
export function orderPredicates(
  predicates: readonly PredicateInput[],
  options?: OrderOptions,
): OrderedPredicates;
export function orderPredicates(
  policies: readonly PredicateInput[],
  users: readonly PredicateInput[],
  options?: OrderOptions,
): OrderedPredicates;
export function orderPredicates(
  input: PredicatePlanInput,
  options?: OrderOptions,
): OrderedPredicates;
export function orderPredicates(
  policyInput: readonly PredicateInput[] | PredicatePlanInput,
  userInput?: readonly PredicateInput[] | OrderOptions,
  maybeOptions?: OrderOptions,
): OrderedPredicates {
  return buildPlan(policyInput, userInput, maybeOptions).order;
}

export const sortPredicates = orderPredicates;
export const orderSecurityPredicates = orderPredicates;
export const getPredicateOrder = orderPredicates;

/**
 * Audits an already-built execution order. A non-leakproof user predicate
 * before the complete policy gate is a violation. A plan without any policy
 * gate is also rejected: it cannot prove that rows were filtered first.
 */
export function checkPredicateOrder(
  order: readonly PredicateInput[],
  options: OrderOptions = {},
): PredicateOrderValidation {
  if (!Array.isArray(order)) {
    throw new PredicateInputError("Predicate order must be an array.");
  }

  const normalized = order.map((predicate, index) =>
    normalizePredicate(predicate, undefined, index, options),
  );
  const policies = normalized.filter(
    (predicate) => predicate.kind === "policy",
  );
  const isDefaultDenyGate = (predicate: NormalizedPredicate): boolean =>
    predicate.kind === "policy" &&
    (predicate.input.defaultDeny === true ||
      predicate.id ===
        (options.defaultDenyPredicateId ?? DEFAULT_DENY_PREDICATE_ID));
  const declaredPolicies = policies.filter(
    (predicate) => !isDefaultDenyGate(predicate),
  );
  const permissive = declaredPolicies.filter(
    (predicate) => predicate.mode === "permissive",
  );
  const restrictive = declaredPolicies.filter(
    (predicate) => predicate.mode === "restrictive",
  );
  const defaultDenyIndex = normalized.findIndex(
    isDefaultDenyGate,
  );
  let lastPolicyIndex = -1;
  for (const predicate of normalized) {
    if (predicate.kind === "policy") {
      lastPolicyIndex = predicate.index;
    }
  }
  const hasPermissive = permissive.length > 0;
  const violations: UnsafeOrderViolation[] = [];

  for (const predicate of normalized) {
    if (predicate.kind !== "user" || predicate.leakproof) continue;

    if (defaultDenyIndex !== -1 && predicate.index > defaultDenyIndex) {
      continue;
    }

    if (lastPolicyIndex === -1) {
      violations.push({
        type: "missing-policy-gate",
        predicateId: predicate.id,
        message: `Non-leakproof predicate ${predicate.id} has no policy gate before it.`,
      });
      continue;
    }

    if (predicate.index <= lastPolicyIndex) {
      violations.push({
        type: "unsafe-user-before-policy",
        predicateId: predicate.id,
        message: `Non-leakproof predicate ${predicate.id} runs before the complete policy gate.`,
      });
      continue;
    }

    if (!hasPermissive && defaultDenyIndex === -1) {
      violations.push({
        type: "missing-default-deny",
        predicateId: predicate.id,
        message: `Predicate ${predicate.id} runs after restrictive-only policies without an explicit default-deny gate.`,
      });
    }
  }

  const valid = violations.length === 0;
  return {
    valid,
    violations,
    defaultDeny: !hasPermissive,
    permissiveCount: permissive.length,
    restrictiveCount: restrictive.length,
  };
}

export const validatePlan = checkPredicateOrder;
export const checkPlanSafety = checkPredicateOrder;

/**
 * Throws UnsafePredicateOrderError when the provided plan can expose hidden
 * rows to a non-leakproof function. Returns true for a safe order.
 */
export function validatePredicateOrder(
  order: readonly PredicateInput[],
  options: OrderOptions = {},
): true {
  const validation = checkPredicateOrder(order, options);
  if (!validation.valid) {
    throw new UnsafePredicateOrderError(validation);
  }
  return true;
}

export const assertSafePredicateOrder = validatePredicateOrder;
export const assertSafePlan = validatePredicateOrder;

export function isPredicateOrderSafe(
  order: readonly PredicateInput[],
  options: OrderOptions = {},
): boolean {
  return checkPredicateOrder(order, options).valid;
}

export const isPlanSafe = isPredicateOrderSafe;

/** Tests a single predicate's leakproof classification. */
export function isLeakproofPredicate(
  predicate: PredicateInput,
  registry?: Readonly<Record<string, boolean | FunctionInfo>>,
): boolean {
  const expression = predicateExpression(predicate, 0);
  return predicateLeakproof(predicate, expression, registry);
}

export const isLeakProofFunction = isLeakproofFunction;
export const isLeakProofPredicate = isLeakproofPredicate;

export default {
  orderPredicates,
  planPredicateOrder,
  mergePolicies,
  buildPolicyExpression,
  validatePredicateOrder,
  checkPredicateOrder,
};
