import { ExpandError } from "./errors.ts";
import {
  canonicalize,
  digestOf,
  isJsonValue,
  pathJoin,
  parsePath,
  type JsonValue,
} from "./json.ts";

/** A condition over parameters; evaluated against a (partial) assignment. */
export type Condition =
  | { param: string; equals: JsonValue }
  | { param: string; in: readonly JsonValue[] }
  | { not: Condition }
  | { all: readonly Condition[] }
  | { any: readonly Condition[] };

/** Finite-domain dependency (implies) or mutual exclusion (mutex). */
export type Constraint =
  | { kind: "implies"; if: Condition; then: Condition }
  | { kind: "mutex"; conditions: readonly Condition[] };

export interface AxisInput {
  name: string;
  /** Ordered finite domain. Arrays are ordered; an empty domain yields 0 rows. */
  domain: readonly JsonValue[];
  /** Optional version tag, bound into cursors. */
  version?: string;
}

export interface ExpandInput {
  /** JSON tree; `{ "$param": "axisName" }` nodes are substituted. */
  template: JsonValue;
  templateVersion?: string;
  /** Axis declaration order fixes the enumeration order (last axis fastest). */
  axes: readonly AxisInput[];
  constraints?: readonly Constraint[];
  /** Record paths (e.g. "$.user.id") declaring the dedup identity. */
  identity?: readonly string[];
}

export interface NormalizedSpec {
  template: JsonValue;
  templateVersion: string | undefined;
  axes: readonly AxisInput[];
  constraints: readonly Constraint[];
  identity: readonly string[];
  /** Binds template + parameter versions and the full input shape. */
  digest: string;
}

function validateCondition(
  cond: unknown,
  known: ReadonlySet<string>,
  path: string,
): void {
  if (cond === null || typeof cond !== "object" || Array.isArray(cond)) {
    throw new ExpandError("constraints", path, "condition must be an object");
  }
  const c = cond as Record<string, unknown>;
  if (typeof c.param === "string") {
    if (!known.has(c.param)) {
      throw new ExpandError(
        "constraints",
        pathJoin(path, "param"),
        `unknown parameter '${c.param}'`,
      );
    }
    if ("equals" in c) {
      if (!isJsonValue(c.equals)) {
        throw new ExpandError(
          "constraints",
          pathJoin(path, "equals"),
          "equals must be a JSON value",
        );
      }
    } else if ("in" in c) {
      if (!Array.isArray(c.in) || !c.in.every(isJsonValue)) {
        throw new ExpandError(
          "constraints",
          pathJoin(path, "in"),
          "in must be an array of JSON values",
        );
      }
    } else {
      throw new ExpandError(
        "constraints",
        path,
        "param condition needs 'equals' or 'in'",
      );
    }
    return;
  }
  if ("not" in c) return validateCondition(c.not, known, pathJoin(path, "not"));
  for (const key of ["all", "any"] as const) {
    if (key in c) {
      const list = c[key];
      if (!Array.isArray(list)) {
        throw new ExpandError(
          "constraints",
          pathJoin(path, key),
          `'${key}' must be an array of conditions`,
        );
      }
      list.forEach((sub, i) =>
        validateCondition(sub, known, pathJoin(pathJoin(path, key), i)),
      );
      return;
    }
  }
  throw new ExpandError("constraints", path, "unrecognized condition shape");
}

function validateTemplate(node: unknown, known: ReadonlySet<string>, path: string): void {
  if (node === null) return;
  switch (typeof node) {
    case "boolean":
    case "string":
      return;
    case "number":
      if (!Number.isFinite(node)) {
        throw new ExpandError("template", path, "number is not JSON-compatible");
      }
      return;
    case "object": {
      if (Array.isArray(node)) {
        node.forEach((item, i) => validateTemplate(item, known, pathJoin(path, i)));
        return;
      }
      const obj = node as Record<string, unknown>;
      if ("$param" in obj) {
        const keys = Object.keys(obj);
        if (keys.length !== 1) {
          throw new ExpandError(
            "template",
            path,
            "$param node must not have sibling keys",
          );
        }
        const name = obj.$param;
        if (typeof name !== "string" || !known.has(name)) {
          throw new ExpandError(
            "template",
            pathJoin(path, "$param"),
            `unknown parameter '${String(name)}'`,
          );
        }
        return;
      }
      for (const [key, value] of Object.entries(obj)) {
        validateTemplate(value, known, pathJoin(path, key));
      }
      return;
    }
    default:
      throw new ExpandError("template", path, `value of type '${typeof node}' is not JSON-compatible`);
  }
}

/** Validate the raw input and precompute the binding digest. */
export function normalizeSpec(input: ExpandInput): NormalizedSpec {
  if (input === null || typeof input !== "object") {
    throw new ExpandError("params", "$", "input must be an object");
  }
  const axes = input.axes;
  if (!Array.isArray(axes)) {
    throw new ExpandError("params", "$.axes", "axes must be an array (declaration order matters)");
  }
  const known = new Set<string>();
  axes.forEach((axis, i) => {
    const path = pathJoin("$.axes", i);
    if (axis === null || typeof axis !== "object") {
      throw new ExpandError("params", path, "axis must be an object");
    }
    if (typeof axis.name !== "string" || axis.name === "") {
      throw new ExpandError("params", pathJoin(path, "name"), "axis name must be a non-empty string");
    }
    if (known.has(axis.name)) {
      throw new ExpandError("params", pathJoin(path, "name"), `duplicate axis name '${axis.name}'`);
    }
    if (!Array.isArray(axis.domain)) {
      throw new ExpandError("params", pathJoin(path, "domain"), "domain must be an array");
    }
    axis.domain.forEach((value, j) => {
      if (!isJsonValue(value)) {
        throw new ExpandError(
          "params",
          pathJoin(pathJoin(path, "domain"), j),
          "domain value is not JSON-compatible",
        );
      }
    });
    known.add(axis.name);
  });

  validateTemplate(input.template, known, "$.template");

  const constraints = input.constraints ?? [];
  constraints.forEach((constraint, i) => {
    const path = pathJoin("$.constraints", i);
    if (constraint === null || typeof constraint !== "object") {
      throw new ExpandError("constraints", path, "constraint must be an object");
    }
    if (constraint.kind === "implies") {
      validateCondition(constraint.if, known, pathJoin(path, "if"));
      validateCondition(constraint.then, known, pathJoin(path, "then"));
    } else if (constraint.kind === "mutex") {
      if (!Array.isArray(constraint.conditions) || constraint.conditions.length < 2) {
        throw new ExpandError(
          "constraints",
          pathJoin(path, "conditions"),
          "mutex needs at least two conditions",
        );
      }
      constraint.conditions.forEach((cond, j) =>
        validateCondition(cond, known, pathJoin(pathJoin(path, "conditions"), j)),
      );
    } else {
      throw new ExpandError("constraints", pathJoin(path, "kind"), "kind must be 'implies' or 'mutex'");
    }
  });

  const identity = input.identity ?? [];
  identity.forEach((p, i) => {
    const path = pathJoin("$.identity", i);
    try {
      parsePath(p);
    } catch (err) {
      throw new ExpandError("identity", path, `bad identity path: ${(err as Error).message}`);
    }
  });

  const digest = digestOf(
    canonicalize({
      template: input.template,
      templateVersion: input.templateVersion ?? null,
      axes: axes.map((a) => ({
        name: a.name,
        domain: a.domain as JsonValue[],
        version: a.version ?? null,
      })),
      constraints: constraints as unknown as JsonValue,
      identity: identity as JsonValue[],
    } as JsonValue),
  );

  return {
    template: input.template,
    templateVersion: input.templateVersion,
    axes,
    constraints,
    identity,
    digest,
  };
}
