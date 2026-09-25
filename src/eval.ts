import type { Node } from "./ast.ts";
import { FALSE, MISSING, TRUE, triAnd, triNot, triOr } from "./trivalue.ts";
import type { TriValue } from "./trivalue.ts";
import { rangesContain } from "./ranges.ts";
import type { Assignment, Schema } from "./schema.ts";
import { assertDeclared, validateAssignment } from "./schema.ts";

/**
 * Evaluate under strong-Kleene three-valued semantics.
 *
 *   const       -> its literal value
 *   leaf on an attribute that is absent -> MISSING
 *   NOT / AND / OR -> triNot / triAnd / triOr
 */
export function evaluate(expr: Node, assignment: Assignment, schema: Schema): TriValue {
  assertDeclared(expr, schema);
  validateAssignment(assignment, schema);
  return evalNode(expr, assignment);
}

function evalNode(node: Node, a: Assignment): TriValue {
  switch (node.kind) {
    case "const":
      return node.value === 1 ? TRUE : FALSE;
    case "bool": {
      const v = a[node.name];
      if (v === undefined) return MISSING;
      return v ? TRUE : FALSE;
    }
    case "in": {
      const v = a[node.name];
      if (v === undefined) return MISSING;
      return node.set.has(v as string) ? TRUE : FALSE;
    }
    case "cmp": {
      const v = a[node.name];
      if (v === undefined) return MISSING;
      return rangesContain(node.ranges, v as number) ? TRUE : FALSE;
    }
    case "not":
      return triNot(evalNode(node.arg, a));
    case "and": {
      let acc: TriValue = TRUE;
      for (const child of node.args) acc = triAnd(acc, evalNode(child, a));
      return acc;
    }
    case "or": {
      let acc: TriValue = FALSE;
      for (const child of node.args) acc = triOr(acc, evalNode(child, a));
      return acc;
    }
  }
}
