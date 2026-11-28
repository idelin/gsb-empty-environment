export type LiteralKind = "Int" | "String" | "Bool" | "Unit";

export interface LiteralExpr {
  readonly tag: "lit";
  readonly kind: LiteralKind;
  readonly value: number | string | boolean | null;
}

export interface VariableExpr {
  readonly tag: "var";
  readonly name: string;
}

export interface LambdaExpr {
  readonly tag: "lambda";
  readonly param: string;
  readonly body: Expr;
}

export interface ApplyExpr {
  readonly tag: "app";
  readonly fn: Expr;
  readonly arg: Expr;
}

export interface LetExpr {
  readonly tag: "let";
  readonly name: string;
  readonly value: Expr;
  readonly body: Expr;
  readonly recursive?: boolean;
}

export interface RecordField {
  readonly label: string;
  readonly value: Expr;
}

/**
 * A record constructor.  With no tail it is closed.  With a tail it is the
 * row extension `{ label = value, ... | tail }`.
 */
export interface RecordExpr {
  readonly tag: "record";
  readonly fields: readonly RecordField[];
  readonly tail?: Expr;
}

export interface ProjectExpr {
  readonly tag: "project";
  readonly record: Expr;
  readonly label: string;
}

export interface RemoveExpr {
  readonly tag: "remove";
  readonly record: Expr;
  readonly label: string;
}

export interface RefExpr {
  readonly tag: "ref";
  readonly value: Expr;
}

export interface DerefExpr {
  readonly tag: "deref";
  readonly value: Expr;
}

export interface AssignExpr {
  readonly tag: "assign";
  readonly target: Expr;
  readonly value: Expr;
}

export interface SequenceExpr {
  readonly tag: "seq";
  readonly first: Expr;
  readonly rest: Expr;
}

export interface TupleExpr {
  readonly tag: "tuple";
  readonly items: readonly Expr[];
}

export interface ListExpr {
  readonly tag: "list";
  readonly items: readonly Expr[];
}

export interface IfExpr {
  readonly tag: "if";
  readonly condition: Expr;
  readonly thenBranch: Expr;
  readonly elseBranch: Expr;
}

export interface AnnotateExpr {
  readonly tag: "annotate";
  readonly expr: Expr;
  readonly type: import("./types.ts").Type;
}

export type Expr =
  | LiteralExpr
  | VariableExpr
  | LambdaExpr
  | ApplyExpr
  | LetExpr
  | RecordExpr
  | ProjectExpr
  | RemoveExpr
  | RefExpr
  | DerefExpr
  | AssignExpr
  | SequenceExpr
  | TupleExpr
  | ListExpr
  | IfExpr
  | AnnotateExpr;

export function intLiteral(value: number): LiteralExpr {
  return { tag: "lit", kind: "Int", value };
}

export function stringLiteral(value: string): LiteralExpr {
  return { tag: "lit", kind: "String", value };
}

export function boolLiteral(value: boolean): LiteralExpr {
  return { tag: "lit", kind: "Bool", value };
}

export function unitLiteral(): LiteralExpr {
  return { tag: "lit", kind: "Unit", value: null };
}

export const int = intLiteral;
export const integer = intLiteral;
export const num = intLiteral;
export const string = stringLiteral;
export const str = stringLiteral;
export const bool = boolLiteral;
export const boolean = boolLiteral;
export const unit = unitLiteral;
export const unitValue = unitLiteral;

export function variable(name: string): VariableExpr {
  return { tag: "var", name };
}

export const varExpr = variable;
export const variableExpr = variable;
export const v = variable;
export const var_ = variable;

export function lambda(param: string, body: Expr): LambdaExpr {
  return { tag: "lambda", param, body };
}

export const fn = lambda;
export const lam = lambda;
export const lambdaExpr = lambda;

export function apply(fnExpr: Expr, arg: Expr): ApplyExpr {
  return { tag: "app", fn: fnExpr, arg };
}

export const app = apply;
export const ap = apply;
export const application = apply;

export function applyMany(fnExpr: Expr, args: readonly Expr[]): Expr {
  return args.reduce<Expr>((current, arg) => apply(current, arg), fnExpr);
}

export function letIn(
  name: string,
  value: Expr,
  body: Expr,
  recursive = false,
): LetExpr {
  return recursive
    ? { tag: "let", name, value, body, recursive: true }
    : { tag: "let", name, value, body };
}

export const let_ = letIn;
export const letExpr = letIn;
export const letBinding = letIn;

function normalizeFields(
  fields:
    | Readonly<Record<string, Expr>>
    | readonly RecordField[]
    | ReadonlyArray<readonly [string, Expr]>,
): RecordField[] {
  if (Array.isArray(fields)) {
    return fields.map((entry) => {
      if (Array.isArray(entry)) {
        return { label: entry[0], value: entry[1] };
      }
      return entry as RecordField;
    });
  }
  return Object.entries(fields as Record<string, Expr>).map(
    ([label, value]) => ({ label, value }),
  );
}

export function record(
  fields:
    | Readonly<Record<string, Expr>>
    | readonly RecordField[]
    | ReadonlyArray<readonly [string, Expr]> = {},
  tail?: Expr,
): RecordExpr {
  const normalized = normalizeFields(fields);
  return tail === undefined
    ? { tag: "record", fields: normalized }
    : { tag: "record", fields: normalized, tail };
}

export const recordExpr = record;
export const recordConstructor = record;

export function extend(
  label: string,
  value: Expr,
  tail: Expr,
): RecordExpr {
  return record([{ label, value }], tail);
}

export const extendExpr = extend;
export const recordExtension = extend;

export function project(recordExpr: Expr, label: string): ProjectExpr {
  return { tag: "project", record: recordExpr, label };
}

export const projection = project;
export const fieldProjection = project;

export function remove(recordExpr: Expr, label: string): RemoveExpr {
  return { tag: "remove", record: recordExpr, label };
}

export const deleteField = remove;
export const removeField = remove;
export const fieldRemoval = remove;

export function ref(value: Expr): RefExpr {
  return { tag: "ref", value };
}

export const refExpr = ref;
export const newRef = ref;

export function deref(value: Expr): DerefExpr {
  return { tag: "deref", value };
}

export const dereference = deref;
export const derefExpr = deref;

export function assign(target: Expr, value: Expr): AssignExpr {
  return { tag: "assign", target, value };
}

export const assignment = assign;
export const assignExpr = assign;

export function sequence(first: Expr, rest: Expr): SequenceExpr {
  return { tag: "seq", first, rest };
}

export const seq = sequence;
export const sequenceExpr = sequence;

export function tuple(items: readonly Expr[]): TupleExpr {
  return { tag: "tuple", items };
}

export const tupleExpr = tuple;

export function list(items: readonly Expr[]): ListExpr {
  return { tag: "list", items };
}

export const listExpr = list;

export function conditional(
  condition: Expr,
  thenBranch: Expr,
  elseBranch: Expr,
): IfExpr {
  return { tag: "if", condition, thenBranch, elseBranch };
}

export const ifThenElse = conditional;
export const ifExpr = conditional;

export function annotate(expr: Expr, type: import("./types.ts").Type): AnnotateExpr {
  return { tag: "annotate", expr, type };
}

export const annotation = annotate;
export const typeAnnotation = annotate;
