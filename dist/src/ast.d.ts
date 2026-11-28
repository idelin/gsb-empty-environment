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
    readonly type: import("./types.js").Type;
}
export type Expr = LiteralExpr | VariableExpr | LambdaExpr | ApplyExpr | LetExpr | RecordExpr | ProjectExpr | RemoveExpr | RefExpr | DerefExpr | AssignExpr | SequenceExpr | TupleExpr | ListExpr | IfExpr | AnnotateExpr;
export declare function intLiteral(value: number): LiteralExpr;
export declare function stringLiteral(value: string): LiteralExpr;
export declare function boolLiteral(value: boolean): LiteralExpr;
export declare function unitLiteral(): LiteralExpr;
export declare const int: typeof intLiteral;
export declare const integer: typeof intLiteral;
export declare const num: typeof intLiteral;
export declare const string: typeof stringLiteral;
export declare const str: typeof stringLiteral;
export declare const bool: typeof boolLiteral;
export declare const boolean: typeof boolLiteral;
export declare const unit: typeof unitLiteral;
export declare const unitValue: typeof unitLiteral;
export declare function variable(name: string): VariableExpr;
export declare const varExpr: typeof variable;
export declare const variableExpr: typeof variable;
export declare const v: typeof variable;
export declare const var_: typeof variable;
export declare function lambda(param: string, body: Expr): LambdaExpr;
export declare const fn: typeof lambda;
export declare const lam: typeof lambda;
export declare const lambdaExpr: typeof lambda;
export declare function apply(fnExpr: Expr, arg: Expr): ApplyExpr;
export declare const app: typeof apply;
export declare const ap: typeof apply;
export declare const application: typeof apply;
export declare function applyMany(fnExpr: Expr, args: readonly Expr[]): Expr;
export declare function letIn(name: string, value: Expr, body: Expr, recursive?: boolean): LetExpr;
export declare const let_: typeof letIn;
export declare const letExpr: typeof letIn;
export declare const letBinding: typeof letIn;
export declare function record(fields?: Readonly<Record<string, Expr>> | readonly RecordField[] | ReadonlyArray<readonly [string, Expr]>, tail?: Expr): RecordExpr;
export declare const recordExpr: typeof record;
export declare const recordConstructor: typeof record;
export declare function extend(label: string, value: Expr, tail: Expr): RecordExpr;
export declare const extendExpr: typeof extend;
export declare const recordExtension: typeof extend;
export declare function project(recordExpr: Expr, label: string): ProjectExpr;
export declare const projection: typeof project;
export declare const fieldProjection: typeof project;
export declare function remove(recordExpr: Expr, label: string): RemoveExpr;
export declare const deleteField: typeof remove;
export declare const removeField: typeof remove;
export declare const fieldRemoval: typeof remove;
export declare function ref(value: Expr): RefExpr;
export declare const refExpr: typeof ref;
export declare const newRef: typeof ref;
export declare function deref(value: Expr): DerefExpr;
export declare const dereference: typeof deref;
export declare const derefExpr: typeof deref;
export declare function assign(target: Expr, value: Expr): AssignExpr;
export declare const assignment: typeof assign;
export declare const assignExpr: typeof assign;
export declare function sequence(first: Expr, rest: Expr): SequenceExpr;
export declare const seq: typeof sequence;
export declare const sequenceExpr: typeof sequence;
export declare function tuple(items: readonly Expr[]): TupleExpr;
export declare const tupleExpr: typeof tuple;
export declare function list(items: readonly Expr[]): ListExpr;
export declare const listExpr: typeof list;
export declare function conditional(condition: Expr, thenBranch: Expr, elseBranch: Expr): IfExpr;
export declare const ifThenElse: typeof conditional;
export declare const ifExpr: typeof conditional;
export declare function annotate(expr: Expr, type: import("./types.js").Type): AnnotateExpr;
export declare const annotation: typeof annotate;
export declare const typeAnnotation: typeof annotate;
