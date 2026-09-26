import { escapeSegment, getAt, joinPath, type Path } from './paths.ts';
import { compare, deepEqual, OP_LABEL, typeOk, type SchemaNode } from './schema.ts';

export interface ValidationError {
  /** Where the problem is, in the instance document. */
  instancePath: string;
  /** Which rule produced it, as a path into the schema ("#" is the schema root). */
  schemaPath: string;
  message: string;
}

/**
 * A compiled, instance-bound validation rule. `deps` lists every instance
 * path the rule reads; the dependency index is built from these paths.
 */
export interface RuleInstance {
  /** Unique id: schema location + "@" + instance scope. */
  id: string;
  schemaPath: string;
  /** Instance path of the node this rule is anchored to. */
  scope: Path;
  /** Instance paths this rule reads. */
  deps: Path[];
  /**
   * True when the rule reads its dependency *deeply* (enum deep-equality):
   * any write inside the dependency may change the outcome. Shallow rules
   * only change when the dependency node itself is replaced or removed.
   */
  deep: boolean;
  run(doc: unknown): ValidationError | null;
}

type Check = (doc: unknown) => string | null;

function leafRule(schemaPath: string, iPath: Path, check: Check, deep = false): RuleInstance {
  return {
    id: `${schemaPath}@${iPath}`,
    schemaPath,
    scope: iPath,
    deps: [iPath],
    deep,
    run(doc) {
      const message = check(doc);
      return message === null ? null : { instancePath: iPath, schemaPath, message };
    },
  };
}

function requiredRule(sPath: string, iPath: Path, key: string): RuleInstance {
  const childPath = joinPath(iPath, key);
  const schemaPath = `${sPath}/required`;
  return {
    id: `${schemaPath}#${escapeSegment(key)}@${iPath}`,
    schemaPath,
    scope: iPath,
    deps: [childPath],
    deep: false,
    run(doc) {
      if (getAt(doc, childPath) !== undefined) return null;
      return { instancePath: childPath, schemaPath, message: `missing required property "${key}"` };
    },
  };
}

function resolveRel(base: Path, rel: string): Path {
  if (rel === '' || rel === '.') return base;
  return rel.split('/').reduce((p, seg) => joinPath(p, seg), base);
}

function crossRule(sPath: string, iPath: Path, node: SchemaNode, index: number): RuleInstance {
  const cr = node.rules![index];
  const left = resolveRel(iPath, cr.left);
  const right = resolveRel(iPath, cr.right);
  const schemaPath = `${sPath}/rules/${index}`;
  return {
    id: `${schemaPath}@${iPath}`,
    schemaPath,
    scope: iPath,
    deps: [left, right],
    deep: false,
    run(doc) {
      const lv = getAt(doc, left);
      const rv = getAt(doc, right);
      if (typeof lv !== 'number' || typeof rv !== 'number') return null;
      if (compare(cr.op, lv, rv)) return null;
      return {
        instancePath: left,
        schemaPath,
        message: `expected ${cr.left} ${OP_LABEL[cr.op]} ${cr.right}, got ${lv} vs ${rv}`,
      };
    },
  };
}

/**
 * Walk schema and document together, emitting one rule per applicable
 * constraint. Children of a type-mismatched node are not expanded, so a
 * single type error replaces a cascade of child errors.
 */
export function expandNode(
  node: SchemaNode,
  sPath: string,
  value: unknown,
  iPath: Path,
  add: (rule: RuleInstance) => void,
): void {
  if (value === undefined) return;

  if (node.type !== undefined) {
    const t = node.type;
    add(leafRule(`${sPath}/type`, iPath, (doc) => {
      const v = getAt(doc, iPath);
      return typeOk(t, v) ? null : `expected type ${t}, got ${Array.isArray(v) ? 'array' : typeof v}`;
    }));
    if (!typeOk(t, value)) return;
  }

  if (node.enum !== undefined) {
    const allowed = node.enum;
    add(leafRule(`${sPath}/enum`, iPath, (doc) => {
      const v = getAt(doc, iPath);
      return allowed.some((e) => deepEqual(e, v)) ? null : `value not in enum ${JSON.stringify(allowed)}`;
    }, true));
  }

  const isObj = typeof value === 'object' && value !== null && !Array.isArray(value);
  const isArr = Array.isArray(value);
  const kind = node.type ?? (isObj ? 'object' : isArr ? 'array' : typeof value);

  if (kind === 'object' && isObj) {
    const obj = value as Record<string, unknown>;
    for (const key of node.required ?? []) add(requiredRule(sPath, iPath, key));
    for (const [key, child] of Object.entries(node.properties ?? {})) {
      if (key in obj) {
        expandNode(child, `${sPath}/properties/${escapeSegment(key)}`, obj[key], joinPath(iPath, key), add);
      }
    }
    for (let i = 0; i < (node.rules ?? []).length; i++) add(crossRule(sPath, iPath, node, i));
    return;
  }

  if (kind === 'array' && isArr) {
    const arr = value as unknown[];
    if (node.minItems !== undefined) {
      const min = node.minItems;
      add(leafRule(`${sPath}/minItems`, iPath, (doc) => {
        const v = getAt(doc, iPath);
        return Array.isArray(v) && v.length >= min ? null : `array has fewer than ${min} items`;
      }));
    }
    if (node.maxItems !== undefined) {
      const max = node.maxItems;
      add(leafRule(`${sPath}/maxItems`, iPath, (doc) => {
        const v = getAt(doc, iPath);
        return Array.isArray(v) && v.length <= max ? null : `array has more than ${max} items`;
      }));
    }
    if (node.items) {
      arr.forEach((item, idx) => expandNode(node.items!, `${sPath}/items`, item, joinPath(iPath, idx), add));
    }
    return;
  }

  if (kind === 'string' && typeof value === 'string') {
    if (node.minLength !== undefined) {
      const min = node.minLength;
      add(leafRule(`${sPath}/minLength`, iPath, (doc) => {
        const v = getAt(doc, iPath);
        return typeof v === 'string' && v.length >= min ? null : `string shorter than minLength ${min}`;
      }));
    }
    if (node.maxLength !== undefined) {
      const max = node.maxLength;
      add(leafRule(`${sPath}/maxLength`, iPath, (doc) => {
        const v = getAt(doc, iPath);
        return typeof v === 'string' && v.length <= max ? null : `string longer than maxLength ${max}`;
      }));
    }
    if (node.pattern !== undefined) {
      const re = new RegExp(node.pattern);
      add(leafRule(`${sPath}/pattern`, iPath, (doc) => {
        const v = getAt(doc, iPath);
        return typeof v === 'string' && re.test(v) ? null : `string does not match pattern ${node.pattern}`;
      }));
    }
    return;
  }

  if ((kind === 'number' || kind === 'integer') && typeof value === 'number') {
    const bounds: Array<['minimum' | 'maximum' | 'exclusiveMinimum' | 'exclusiveMaximum', (v: number, b: number) => boolean, string]> = [
      ['minimum', (v, b) => v >= b, '>='],
      ['maximum', (v, b) => v <= b, '<='],
      ['exclusiveMinimum', (v, b) => v > b, '>'],
      ['exclusiveMaximum', (v, b) => v < b, '<'],
    ];
    for (const [keyword, ok, label] of bounds) {
      const bound = node[keyword];
      if (bound === undefined) continue;
      add(leafRule(`${sPath}/${keyword}`, iPath, (doc) => {
        const v = getAt(doc, iPath);
        return typeof v === 'number' && ok(v, bound) ? null : `number must be ${label} ${bound}, got ${v}`;
      }));
    }
  }
}

/** Locate the schema node governing an instance path (for re-expansion). */
export function schemaAtPath(
  schema: SchemaNode,
  p: Path,
): { node: SchemaNode; sPath: string } | undefined {
  let node = schema;
  let sPath = '#';
  for (const seg of p === '' ? [] : p.slice(1).split('/')) {
    const key = seg.replace(/~1/g, '/').replace(/~0/g, '~');
    if (node.type === 'object' && node.properties && key in node.properties) {
      node = node.properties[key];
      sPath += `/properties/${escapeSegment(key)}`;
    } else if ((node.type === 'array' || node.items) && node.items && /^\d+$/.test(key)) {
      node = node.items;
      sPath += '/items';
    } else {
      return undefined;
    }
  }
  return { node, sPath };
}
