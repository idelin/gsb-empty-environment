import type { Expr, RecordField } from "./ast.ts";
import { parse } from "./parser.ts";
import type {
  EnvInput,
  EnvValue,
  Environment,
  Row,
  Scheme,
  Type,
  TypeScheme,
} from "./types.ts";
import {
  boolType,
  emptyRow,
  extendRow,
  fnType,
  freshRowVariable,
  freshTypeVariable,
  generalize,
  HMTypeError,
  intType,
  instantiate,
  isType,
  isTypeScheme,
  listType,
  markWeakType,
  monoScheme,
  recordTypeFromRow,
  refType,
  stringType,
  tupleType,
  unifyTypes,
  unitType,
} from "./types.ts";
import { typeToString } from "./format.ts";

export interface InferOptions {
  readonly includeBuiltins?: boolean;
}

export interface InferenceResult {
  readonly type: Type;
  readonly scheme: TypeScheme;
  readonly typeString: string;
  readonly schemeString: string;
  readonly principalType: string;
}

function polymorphic(type: Type): TypeScheme {
  return generalize(type, new Map(), true);
}

/**
 * The prelude used by the source parser.  All list and ref operations are
 * ordinary HM constants, so their types are instantiated at every use.
 */
export function builtinEnvironment(): Environment {
  const environment: Environment = new Map();

  const nilType = freshTypeVariable();
  environment.set("nil", polymorphic(listType(nilType)));

  const consType = freshTypeVariable();
  environment.set(
    "cons",
    polymorphic(
      fnType(
        consType,
        fnType(listType(consType), listType(consType)),
      ),
    ),
  );

  const headType = freshTypeVariable();
  environment.set(
    "head",
    polymorphic(fnType(listType(headType), headType)),
  );

  const tailType = freshTypeVariable();
  environment.set(
    "tail",
    polymorphic(fnType(listType(tailType), listType(tailType))),
  );

  const isEmptyType = freshTypeVariable();
  environment.set(
    "isEmpty",
    polymorphic(fnType(listType(isEmptyType), boolType)),
  );

  const refElement = freshTypeVariable();
  environment.set(
    "ref",
    polymorphic(fnType(refElement, refType(refElement))),
  );

  const derefElement = freshTypeVariable();
  environment.set(
    "deref",
    polymorphic(fnType(refType(derefElement), derefElement)),
  );
  environment.set("!", environment.get("deref") as TypeScheme);

  const assignElement = freshTypeVariable();
  environment.set(
    ":=",
    polymorphic(
      fnType(
        refType(assignElement),
        fnType(assignElement, unitType),
      ),
    ),
  );

  environment.set("+", polymorphic(fnType(intType, fnType(intType, intType))));
  environment.set("-", environment.get("+") as TypeScheme);
  environment.set("*", environment.get("+") as TypeScheme);
  environment.set("/", environment.get("+") as TypeScheme);
  environment.set("negate", polymorphic(fnType(intType, intType)));
  environment.set("not", polymorphic(fnType(boolType, boolType)));
  environment.set("&&", polymorphic(fnType(boolType, fnType(boolType, boolType))));
  environment.set("||", environment.get("&&") as TypeScheme);

  environment.set(
    "<",
    polymorphic(fnType(intType, fnType(intType, boolType))),
  );
  environment.set("<=", environment.get("<") as TypeScheme);
  environment.set(">", environment.get("<") as TypeScheme);
  environment.set(">=", environment.get("<") as TypeScheme);

  const equalityType = freshTypeVariable();
  environment.set(
    "==",
    polymorphic(
      fnType(
        equalityType,
        fnType(equalityType, boolType),
      ),
    ),
  );

  const inequalityType = freshTypeVariable();
  environment.set(
    "!=",
    polymorphic(
      fnType(
        inequalityType,
        fnType(inequalityType, boolType),
      ),
    ),
  );

  const firstType = freshTypeVariable();
  const secondType = freshTypeVariable();
  environment.set(
    "fst",
    polymorphic(fnType(tupleType([firstType, secondType]), firstType)),
  );
  environment.set(
    "snd",
    polymorphic(fnType(tupleType([firstType, secondType]), secondType)),
  );

  environment.set("unit", monoScheme(unitType));

  return environment;
}

export const defaultEnvironment = builtinEnvironment;
export const builtinEnv = builtinEnvironment;
export const initialEnvironment = builtinEnvironment;

export function emptyEnvironment(): Environment {
  return new Map();
}

export const emptyEnv = emptyEnvironment;

export function normalizeEnvironment(input?: EnvInput): Environment {
  const environment: Environment = new Map();
  if (input === undefined) return environment;
  const entries =
    input instanceof Map
      ? [...input.entries()]
      : Object.entries(input as Record<string, EnvValue>);
  for (const [name, value] of entries) {
    if (isTypeScheme(value)) {
      environment.set(name, value);
    } else if (isType(value)) {
      environment.set(name, monoScheme(value));
    } else {
      throw new HMTypeError(
        "INVALID_TYPE",
        `environment binding '${name}' is neither a type nor a scheme`,
        { name },
      );
    }
  }
  return environment;
}

function createEnvironment(
  input: EnvInput | undefined,
  options: InferOptions,
): Environment {
  const environment =
    options.includeBuiltins === false ? new Map<string, TypeScheme>() : builtinEnvironment();
  for (const [name, scheme] of normalizeEnvironment(input)) {
    environment.set(name, scheme);
  }
  return environment;
}

function extendEnvironment(
  environment: Environment,
  name: string,
  scheme: TypeScheme,
): Environment {
  const extended = new Map(environment);
  extended.set(name, scheme);
  return extended;
}

export const extendEnv = extendEnvironment;

function lookup(environment: Environment, name: string): Type {
  const scheme = environment.get(name);
  if (scheme === undefined) {
    throw new HMTypeError(
      "UNBOUND_VARIABLE",
      `unbound variable '${name}'`,
      { name },
    );
  }
  return instantiate(scheme);
}

function inferRecord(expression: Extract<Expr, { tag: "record" }>, environment: Environment): Type {
  let row: Row = emptyRow;

  if (expression.tail !== undefined) {
    const tailType = inferExpr(expression.tail, environment);
    const tailRow = freshRowVariable();
    unifyTypes(tailType, recordTypeFromRow(tailRow));
    row = tailRow;
  }

  for (const field of expression.fields) {
    row = extendRow(
      field.label,
      inferExpr(field.value, environment),
      row,
    );
  }

  return recordTypeFromRow(row);
}

function inferList(expression: Extract<Expr, { tag: "list" }>, environment: Environment): Type {
  if (expression.items.length === 0) {
    return listType(freshTypeVariable());
  }
  const elementType = inferExpr(expression.items[0], environment);
  for (let index = 1; index < expression.items.length; index += 1) {
    unifyTypes(
      elementType,
      inferExpr(expression.items[index], environment),
    );
  }
  return listType(elementType);
}

function inferExpr(expression: Expr, environment: Environment): Type {
  switch (expression.tag) {
    case "lit":
      switch (expression.kind) {
        case "Int":
          return intType;
        case "String":
          return stringType;
        case "Bool":
          return boolType;
        case "Unit":
          return unitType;
      }

    case "var":
      return lookup(environment, expression.name);

    case "lambda": {
      const parameterType = freshTypeVariable();
      const bodyEnvironment = extendEnvironment(
        environment,
        expression.param,
        monoScheme(parameterType),
      );
      return fnType(
        parameterType,
        inferExpr(expression.body, bodyEnvironment),
      );
    }

    case "app": {
      const functionType = inferExpr(expression.fn, environment);
      const argumentType = inferExpr(expression.arg, environment);
      const resultType = freshTypeVariable();
      unifyTypes(
        functionType,
        fnType(argumentType, resultType),
      );
      return resultType;
    }

    case "let": {
      if (expression.recursive === true) {
        const placeholder = freshTypeVariable();
        const recursiveEnvironment = extendEnvironment(
          environment,
          expression.name,
          monoScheme(placeholder),
        );
        const valueType = inferExpr(
          expression.value,
          recursiveEnvironment,
        );
        unifyTypes(placeholder, valueType);
        const expansive = !isNonExpansive(expression.value);
        if (expansive) markWeakType(valueType);
        const bindingScheme = generalize(
          valueType,
          environment,
          !expansive,
        );
        const bodyEnvironment = extendEnvironment(
          environment,
          expression.name,
          bindingScheme,
        );
        return inferExpr(expression.body, bodyEnvironment);
      }

      const valueType = inferExpr(expression.value, environment);
      const expansive = !isNonExpansive(expression.value);
      if (expansive) markWeakType(valueType);
      const bindingScheme = generalize(valueType, environment, !expansive);
      const bodyEnvironment = extendEnvironment(
        environment,
        expression.name,
        bindingScheme,
      );
      return inferExpr(expression.body, bodyEnvironment);
    }

    case "record":
      return inferRecord(expression, environment);

    case "project": {
      const recordType = inferExpr(expression.record, environment);
      const fieldType = freshTypeVariable();
      const expected = recordTypeFromRow(
        extendRow(expression.label, fieldType, freshRowVariable()),
      );
      unifyTypes(recordType, expected);
      return fieldType;
    }

    case "remove": {
      const recordType = inferExpr(expression.record, environment);
      const fieldType = freshTypeVariable();
      const remainingRow = freshRowVariable();
      const expected = recordTypeFromRow(
        extendRow(expression.label, fieldType, remainingRow),
      );
      unifyTypes(recordType, expected);
      return recordTypeFromRow(remainingRow);
    }

    case "ref": {
      const elementType = inferExpr(expression.value, environment);
      return refType(elementType);
    }

    case "deref": {
      const referenceType = inferExpr(expression.value, environment);
      const elementType = freshTypeVariable();
      unifyTypes(referenceType, refType(elementType));
      return elementType;
    }

    case "assign": {
      const targetType = inferExpr(expression.target, environment);
      const valueType = inferExpr(expression.value, environment);
      unifyTypes(targetType, refType(valueType));
      return unitType;
    }

    case "seq":
      inferExpr(expression.first, environment);
      return inferExpr(expression.rest, environment);

    case "tuple":
      return tupleType(
        expression.items.map((item) => inferExpr(item, environment)),
      );

    case "list":
      return inferList(expression, environment);

    case "if": {
      unifyTypes(
        inferExpr(expression.condition, environment),
        boolType,
      );
      const thenType = inferExpr(expression.thenBranch, environment);
      const elseType = inferExpr(expression.elseBranch, environment);
      unifyTypes(thenType, elseType);
      return thenType;
    }

    case "annotate": {
      const inferred = inferExpr(expression.expr, environment);
      unifyTypes(inferred, expression.type);
      return expression.type;
    }
  }
}

/**
 * The syntactic non-expansive (value) restriction.  Applications (including
 * `ref e`), dereferences, assignments, projections, lets and conditionals are
 * deliberately excluded.  Record/tuple constructors are values exactly when
 * their components are values.
 */
export function isNonExpansive(expression: Expr): boolean {
  switch (expression.tag) {
    case "lit":
    case "var":
    case "lambda":
      return true;
    case "record":
      return (
        expression.fields.every((field: RecordField) =>
          isNonExpansive(field.value),
        ) &&
        (expression.tail === undefined ||
          isNonExpansive(expression.tail))
      );
    case "tuple":
    case "list":
      return expression.items.every(isNonExpansive);
    case "annotate":
      return isNonExpansive(expression.expr);
    case "app":
    case "let":
    case "project":
    case "remove":
    case "ref":
    case "deref":
    case "assign":
    case "seq":
    case "if":
      return false;
  }
}

export const isValueExpression = isNonExpansive;
export const isSyntacticValue = isNonExpansive;
export const isValue = isNonExpansive;
export const isNonExpansiveValue = isNonExpansive;

function parseInput(input: Expr | string): Expr {
  return typeof input === "string" ? parse(input) : input;
}

function inferTypeInEnvironment(
  input: Expr | string,
  environment: Environment,
): Type {
  return inferExpr(parseInput(input), environment);
}

/**
 * Infer the (monomorphic) type of an expression.  Use `inferScheme` for the
 * canonical principal scheme of a closed expression.
 */
export function infer(
  input: Expr | string,
  environmentInput?: EnvInput,
  options: InferOptions = {},
): Type {
  return inferTypeInEnvironment(
    input,
    createEnvironment(environmentInput, options),
  );
}

export const inferType = infer;
export const inferExpressionType = infer;
export const inferExpression = infer;
export const inferAST = infer;
export const inferSource = infer;
export const inferProgram = infer;
export const inferTypeOf = infer;

export function inferScheme(
  input: Expr | string,
  environmentInput?: EnvInput,
  options: InferOptions = {},
): TypeScheme {
  const expression = parseInput(input);
  const environment = createEnvironment(environmentInput, options);
  const type = inferExpr(expression, environment);
  return generalize(type, environment, true);
}

export const principalScheme = inferScheme;
export const inferPrincipalScheme = inferScheme;

export function inferFull(
  input: Expr | string,
  environmentInput?: EnvInput,
  options: InferOptions = {},
): InferenceResult {
  const expression = parseInput(input);
  const environment = createEnvironment(environmentInput, options);
  const type = inferExpr(expression, environment);
  const scheme = generalize(type, environment, true);
  const typeString = typeToString(type);
  const schemeString = typeToString(scheme);
  return {
    type,
    scheme,
    typeString,
    schemeString,
    principalType: schemeString,
  };
}

export const analyze = inferFull;
export const inferResult = inferFull;
export const analyzeExpression = inferFull;

export function principalType(
  input: Expr | string,
  environmentInput?: EnvInput,
  options: InferOptions = {},
): string {
  return typeToString(inferScheme(input, environmentInput, options));
}

export const inferTypeString = principalType;
export const typeOf = principalType;
export const principalTypeText = principalType;

export function check(
  input: Expr | string,
  environmentInput?: EnvInput,
  options: InferOptions = {},
): InferenceResult {
  return inferFull(input, environmentInput, options);
}

export const typecheck = check;
export const typeCheck = check;

export function formatInferredType(
  input: Expr | string,
  environmentInput?: EnvInput,
  options: InferOptions = {},
): string {
  return typeToString(infer(input, environmentInput, options));
}

export const inferAndFormat = formatInferredType;

export class Inferencer {
  private environment: Environment;
  private readonly options: InferOptions;

  constructor(environmentInput?: EnvInput, options: InferOptions = {}) {
    this.environment = createEnvironment(environmentInput, options);
    this.options = options;
  }

  infer(input: Expr | string): Type {
    return infer(input, this.environment, {
      includeBuiltins: false,
      ...this.options,
    });
  }

  inferScheme(input: Expr | string): TypeScheme {
    return inferScheme(input, this.environment, {
      includeBuiltins: false,
      ...this.options,
    });
  }

  bind(name: string, value: Type | TypeScheme): this {
    const scheme = isTypeScheme(value) ? value : monoScheme(value);
    this.environment = extendEnvironment(this.environment, name, scheme);
    return this;
  }

  environmentSnapshot(): Environment {
    return new Map(this.environment);
  }
}

export const Infer = Inferencer;
