/**
 * Attribute schema: every attribute the policy may read is declared here with
 * a FINITE domain. A missing attribute is not "anything" — it is an unknown
 * variable ranging over exactly this finite domain, which is what makes
 * exhaustive / correlated analysis possible.
 *
 * The policy layer reads only attributes declared here; there is no external
 * identity service or free-form lookup.
 */

export type AttributeValue = number | boolean | string;

export type AttributeDomain =
  | { type: 'int'; min: number; max: number }
  | { type: 'bool' }
  | { type: 'enum'; values: readonly string[] };

export type Schema = Readonly<Record<string, AttributeDomain>>;

export function validateSchema(schema: Schema): void {
  for (const [name, dom] of Object.entries(schema)) {
    if (!/^[_a-zA-Z][_a-zA-Z0-9]*$/.test(name)) {
      throw new Error(`invalid attribute name: ${name}`);
    }
    if (dom.type === 'int') {
      if (!Number.isInteger(dom.min) || !Number.isInteger(dom.max) || dom.min > dom.max) {
        throw new Error(`attribute ${name}: int domain needs integer min <= max`);
      }
    } else if (dom.type === 'enum') {
      if (!Array.isArray(dom.values) || dom.values.length === 0) {
        throw new Error(`attribute ${name}: enum domain needs a non-empty values list`);
      }
      if (new Set(dom.values).size !== dom.values.length) {
        throw new Error(`attribute ${name}: enum values must be distinct`);
      }
    } else if (dom.type !== 'bool') {
      throw new Error(`attribute ${name}: unknown domain type`);
    }
  }
}

export function domainValues(dom: AttributeDomain): readonly AttributeValue[] {
  switch (dom.type) {
    case 'int': {
      const out: number[] = [];
      for (let v = dom.min; v <= dom.max; v++) out.push(v);
      return out;
    }
    case 'bool':
      return [false, true];
    case 'enum':
      return dom.values;
  }
}

export function domainSize(dom: AttributeDomain): number {
  switch (dom.type) {
    case 'int':
      return dom.max - dom.min + 1;
    case 'bool':
      return 2;
    case 'enum':
      return dom.values.length;
  }
}

export function isValidValue(dom: AttributeDomain, value: AttributeValue): boolean {
  switch (dom.type) {
    case 'int':
      return typeof value === 'number' && Number.isInteger(value) && value >= dom.min && value <= dom.max;
    case 'bool':
      return typeof value === 'boolean';
    case 'enum':
      return typeof value === 'string' && dom.values.includes(value);
  }
}

export type Attributes = Readonly<Record<string, AttributeValue>>;

/** Validate provided attributes against the schema. Unknown keys are rejected. */
export function validateAttributes(schema: Schema, attrs: Attributes): void {
  for (const [name, value] of Object.entries(attrs)) {
    const dom = schema[name];
    if (!dom) throw new Error(`attribute ${name} is not declared in the schema`);
    if (!isValidValue(dom, value)) {
      throw new Error(`attribute ${name} has out-of-domain value ${JSON.stringify(value)}`);
    }
  }
}
