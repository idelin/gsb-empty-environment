export type PrimitiveTypeName = "Int" | "String" | "Bool" | "Unit";
export interface TypeVar {
    readonly tag: "tvar";
    readonly id: number;
    binding?: Type;
    weak?: boolean;
}
export interface TypeConst {
    readonly tag: "const";
    readonly name: string;
}
export interface FunctionType {
    readonly tag: "fun";
    readonly arg: Type;
    readonly result: Type;
}
export interface RecordType {
    readonly tag: "record";
    readonly row: Row;
}
export interface RefType {
    readonly tag: "ref";
    readonly element: Type;
}
export interface ListType {
    readonly tag: "list";
    readonly element: Type;
}
export interface TupleType {
    readonly tag: "tuple";
    readonly items: readonly Type[];
}
export type Type = TypeVar | TypeConst | FunctionType | RecordType | RefType | ListType | TupleType;
export interface EmptyRow {
    readonly tag: "empty";
}
export interface RowExtension {
    readonly tag: "extend";
    readonly label: string;
    readonly field: Type;
    readonly tail: Row;
}
export interface RowVar {
    readonly tag: "rvar";
    readonly id: number;
    binding?: Row;
    lacks: Set<string>;
    weak?: boolean;
}
export type Row = EmptyRow | RowExtension | RowVar;
/**
 * A type scheme.  `typeVars` and `rowVars` are the variables quantified by
 * this scheme.  The row map stores the variable's `lacks` constraints so that
 * they are copied (not shared) on every instantiation.
 */
export interface TypeScheme {
    readonly tag: "scheme";
    readonly type: Type;
    readonly typeVars: ReadonlySet<number>;
    readonly rowVars: ReadonlyMap<number, ReadonlySet<string>>;
}
export type Scheme = TypeScheme;
export type TypeVariable = TypeVar;
export type RowVariable = RowVar;
export type TypeConstructor = TypeConst;
export type FunctionTypeConstructor = FunctionType;
export type Environment = Map<string, TypeScheme>;
export type Env = Environment;
export type TypeEnv = Environment;
export type MonoType = Type;
export type EnvValue = TypeScheme | Type;
export type EnvInput = ReadonlyMap<string, EnvValue> | Readonly<Record<string, EnvValue>>;
export type HMTypeErrorCode = "TYPE_MISMATCH" | "OCCURS_CHECK" | "ROW_OCCURS_CHECK" | "DUPLICATE_FIELD" | "LACKS_VIOLATION" | "UNBOUND_VARIABLE" | "INVALID_TYPE";
export type TypeErrorCode = HMTypeErrorCode;
export declare class HMTypeError extends Error {
    readonly code: HMTypeErrorCode;
    readonly details: Readonly<Record<string, unknown>>;
    constructor(code: HMTypeErrorCode, message: string, details?: Record<string, unknown>);
}
export declare const emptyRow: EmptyRow;
export declare const intType: TypeConst;
export declare const stringType: TypeConst;
export declare const boolType: TypeConst;
export declare const unitType: TypeConst;
export declare const IntType: TypeConst;
export declare const StringType: TypeConst;
export declare const BoolType: TypeConst;
export declare const UnitType: TypeConst;
export declare function freshTypeVariable(weak?: boolean): TypeVar;
export declare function freshRowVariable(lacks?: Iterable<string>): RowVar;
export declare const tvar: typeof freshTypeVariable;
export declare const freshTypeVar: typeof freshTypeVariable;
export declare const newTypeVariable: typeof freshTypeVariable;
export declare const rowVar: typeof freshRowVariable;
export declare const freshRow: typeof freshRowVariable;
export declare const newRowVariable: typeof freshRowVariable;
export declare function constType(name: string): TypeConst;
export declare function fnType(arg: Type, result: Type): FunctionType;
export declare const funType: typeof fnType;
export declare const arrowType: typeof fnType;
export declare const functionType: typeof fnType;
export declare function refType(element: Type): RefType;
export declare function listType(element: Type): ListType;
export declare function tupleType(items: readonly Type[]): TupleType;
export declare function recordTypeFromRow(row?: Row): RecordType;
export type RowFieldsInput = ReadonlyMap<string, Type> | Readonly<Record<string, Type>> | ReadonlyArray<readonly [string, Type]>;
/**
 * Build a record type.  Labels must be distinct.  If a tail is supplied it is
 * an open row, and `extendRow` records the corresponding `lacks` constraints.
 */
export declare function recordType(fields?: RowFieldsInput, tail?: Row): RecordType;
export declare const recordTypeWithTail: typeof recordType;
export declare function pruneType(type: Type): Type;
export declare function pruneRow(row: Row): Row;
export declare function occursInType(typeVarId: number, type: Type): boolean;
export declare function occursInRow(rowVarId: number, row: Row): boolean;
export interface FlatRow {
    readonly fields: Map<string, Type>;
    readonly tail: EmptyRow | RowVar;
}
/**
 * Flatten a row into a label/type map and an empty or unbound row-variable
 * tail.  This makes field order irrelevant and catches duplicate labels.
 */
export declare function flattenRow(row: Row): FlatRow;
export declare const rowFields: typeof flattenRow;
export declare function requireRowLacks(row: Row, labels: Iterable<string>, code?: "LACKS_VIOLATION" | "DUPLICATE_FIELD"): void;
/**
 * Extend a row with a field.  The tail must lack the label; if it is an
 * unbound row variable this is recorded as a residual constraint.
 */
export declare function extendRow(label: string, field: Type, tail?: Row): Row;
export declare const rowExtend: typeof extendRow;
export declare const rowLacks: typeof requireRowLacks;
export declare function rowFromFields(fields: RowFieldsInput, tail?: Row): Row;
export declare function unifyRowVariables(left: RowVar, right: RowVar): void;
export declare function unifyRows(left: Row, right: Row): void;
export declare function unifyTypes(left: Type, right: Type): void;
export declare const unify: typeof unifyTypes;
export declare const unifyType: typeof unifyTypes;
export declare const unifyRow: typeof unifyRows;
export declare const occursCheckType: typeof occursInType;
export declare const occursCheckRow: typeof occursInRow;
export interface FreeVariables {
    readonly types: Map<number, TypeVar>;
    readonly rows: Map<number, RowVar>;
}
export declare function freeVariablesOfType(type: Type): FreeVariables;
export declare function freeTypeVariableIds(type: Type): Set<number>;
export declare function freeRowVariableIds(type: Type): Set<number>;
export declare function freeVariablesOfScheme(scheme: TypeScheme): FreeVariables;
export declare function freeVariablesOfEnvironment(environment: ReadonlyMap<string, TypeScheme>): FreeVariables;
export interface GeneralizeOptions {
    readonly quantify?: boolean;
    readonly respectWeak?: boolean;
}
/**
 * Generalize a monotype into a scheme.  Variables that are weak, or that occur
 * free in the environment, are deliberately not quantified.
 */
export declare function generalize(type: Type, environment?: ReadonlyMap<string, TypeScheme>, options?: GeneralizeOptions | boolean): TypeScheme;
export declare function monoScheme(type: Type): TypeScheme;
export declare const mono: typeof monoScheme;
export declare const monomorphic: typeof monoScheme;
export declare function polymorphicScheme(type: Type, environment?: ReadonlyMap<string, TypeScheme>): TypeScheme;
export declare const scheme: typeof polymorphicScheme;
export declare const quantify: typeof polymorphicScheme;
export declare function instantiate(schemeValue: TypeScheme): Type;
export declare const instantiateScheme: typeof instantiate;
export declare const instantiateTypeScheme: typeof instantiate;
export declare function isTypeScheme(value: unknown): value is TypeScheme;
export declare function isType(value: unknown): value is Type;
export declare function isRow(value: unknown): value is Row;
export declare function markWeakType(type: Type): void;
export declare function markWeakRow(row: Row): void;
export declare function schemeRowConstraints(schemeValue: TypeScheme): Record<string, readonly string[]>;
