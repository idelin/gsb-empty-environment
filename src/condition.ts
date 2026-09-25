/**
 * Attributes are deliberately small in scope: integers, booleans and finite
 * string sets. Conditions are integer comparisons, boolean checks, finite-set
 * membership and boolean algebra. Nothing else is expressible, and no
 * external identity service is ever consulted.
 */
export type AttributeValue = number | boolean | readonly string[];

export type AttributeType = 'integer' | 'boolean' | 'string-set';

export interface AttributeDescriptor {
  readonly name: string;
  readonly type: AttributeType;
}

export type Condition =
  | {
      readonly op: 'int-eq' | 'int-lt' | 'int-le' | 'int-gt' | 'int-ge';
      readonly attr: string;
      readonly value: number;
    }
  | { readonly op: 'bool-is'; readonly attr: string; readonly value: boolean }
  | { readonly op: 'contains'; readonly attr: string; readonly member: string }
  | { readonly op: 'subset-of'; readonly attr: string; readonly set: readonly string[] }
  | { readonly op: 'and' | 'or'; readonly args: readonly Condition[] }
  | { readonly op: 'not'; readonly arg: Condition };

export interface ConditionFailure {
  /** Attributes referenced without an interpretation descriptor. */
  readonly missingEvidence: readonly string[];
  /** Declared attributes absent from the envelope. */
  readonly missingValues: readonly string[];
  /** Attributes whose value violates the declared type. */
  readonly typeErrors: readonly string[];
}

export type ConditionResult =
  | { readonly ok: true; readonly value: boolean; readonly accessed: readonly string[] }
  | { readonly ok: false; readonly failure: ConditionFailure; readonly accessed: readonly string[] };

interface Accumulator {
  readonly accessed: Set<string>;
  readonly missingEvidence: Set<string>;
  readonly missingValues: Set<string>;
  readonly typeErrors: Set<string>;
}

function lookup(
  attr: string,
  expected: AttributeType,
  attributes: Readonly<Record<string, AttributeValue>>,
  descriptors: ReadonlyMap<string, AttributeDescriptor>,
  accum: Accumulator,
): AttributeValue | undefined {
  accum.accessed.add(attr);
  const descriptor = descriptors.get(attr);
  if (!descriptor) {
    accum.missingEvidence.add(attr);
    return undefined;
  }
  if (descriptor.type !== expected) {
    accum.typeErrors.add(attr);
    return undefined;
  }
  if (!Object.hasOwn(attributes, attr)) {
    accum.missingValues.add(attr);
    return undefined;
  }
  const value = attributes[attr];
  const valid =
    (expected === 'integer' && typeof value === 'number' && Number.isInteger(value)) ||
    (expected === 'boolean' && typeof value === 'boolean') ||
    (expected === 'string-set' &&
      Array.isArray(value) &&
      value.every((item) => typeof item === 'string'));
  if (!valid) {
    accum.typeErrors.add(attr);
    return undefined;
  }
  return value;
}

function evaluate(
  condition: Condition,
  attributes: Readonly<Record<string, AttributeValue>>,
  descriptors: ReadonlyMap<string, AttributeDescriptor>,
  accum: Accumulator,
): boolean | undefined {
  switch (condition.op) {
    case 'int-eq':
    case 'int-lt':
    case 'int-le':
    case 'int-gt':
    case 'int-ge': {
      const value = lookup(condition.attr, 'integer', attributes, descriptors, accum);
      if (value === undefined) return undefined;
      const n = value as number;
      switch (condition.op) {
        case 'int-eq': return n === condition.value;
        case 'int-lt': return n < condition.value;
        case 'int-le': return n <= condition.value;
        case 'int-gt': return n > condition.value;
        case 'int-ge': return n >= condition.value;
      }
      break;
    }
    case 'bool-is': {
      const value = lookup(condition.attr, 'boolean', attributes, descriptors, accum);
      return value === undefined ? undefined : value === condition.value;
    }
    case 'contains': {
      const value = lookup(condition.attr, 'string-set', attributes, descriptors, accum);
      return value === undefined ? undefined : (value as readonly string[]).includes(condition.member);
    }
    case 'subset-of': {
      const value = lookup(condition.attr, 'string-set', attributes, descriptors, accum);
      return value === undefined
        ? undefined
        : (value as readonly string[]).every((member) => condition.set.includes(member));
    }
    case 'and':
    case 'or': {
      // Evaluate every argument to collect complete evidence; any failed
      // argument makes the whole node indeterminate.
      const values = condition.args.map((arg) => evaluate(arg, attributes, descriptors, accum));
      if (values.some((value) => value === undefined)) return undefined;
      return condition.op === 'and'
        ? values.every(Boolean)
        : values.some(Boolean);
    }
    case 'not': {
      const value = evaluate(condition.arg, attributes, descriptors, accum);
      return value === undefined ? undefined : !value;
    }
  }
}

export function evaluateCondition(
  condition: Condition,
  attributes: Readonly<Record<string, AttributeValue>>,
  descriptors: ReadonlyMap<string, AttributeDescriptor>,
): ConditionResult {
  const accum: Accumulator = {
    accessed: new Set(),
    missingEvidence: new Set(),
    missingValues: new Set(),
    typeErrors: new Set(),
  };
  const value = evaluate(condition, attributes, descriptors, accum);
  const accessed = [...accum.accessed].sort();
  if (
    value === undefined ||
    accum.missingEvidence.size > 0 ||
    accum.missingValues.size > 0 ||
    accum.typeErrors.size > 0
  ) {
    return {
      ok: false,
      failure: {
        missingEvidence: [...accum.missingEvidence].sort(),
        missingValues: [...accum.missingValues].sort(),
        typeErrors: [...accum.typeErrors].sort(),
      },
      accessed,
    };
  }
  return { ok: true, value, accessed };
}
