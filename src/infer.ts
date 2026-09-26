// Inductive inference of schema CANDIDATES from evidence.
// Two profiles are emitted (a deliberately small, bounded candidate set):
//   - "observed-fit": tightest schema that fits the retained samples exactly
//     (integer types, closed ranges, required keys, enums). Every one of
//     these constraints is an inductive assumption, flagged as such.
//   - "generalized": drops every inductive assumption that is not forced by
//     evidence (number instead of integer, no ranges, all keys optional).
// Constraints FORCED by retained samples (nullable when nulls were seen,
// optional when absences were seen, number when decimals were seen, unions
// when types were mixed) appear in BOTH profiles so that every candidate
// accepts every retained sample -- this is verified independently.

import type { EvidenceNode } from "./evidence.ts";
import type { Axis, PrimitiveType, Schema, Uncertainty } from "./types.ts";

const ENUM_CANDIDATE_CAP = 5;

export interface FieldSchemas {
  strict: Schema;
  generalized: Schema;
  axes: Axis[];
  uncertainties: Uncertainty[];
}

function escapePointer(segment: string): string {
  return segment.replace(/~/g, "~0").replace(/\//g, "~1");
}

function addNull(schema: Schema): void {
  if (schema.type === undefined) return; // unknown domain already accepts null
  if (typeof schema.type === "string") {
    schema.type = [schema.type, "null"];
  } else if (!schema.type.includes("null")) {
    schema.type.push("null");
  }
}

function typeList(node: EvidenceNode): [string, number][] {
  return Object.entries(node.typeCounts).filter(([, n]) => n > 0);
}

export function inferNode(node: EvidenceNode, rulePath: string): FieldSchemas {
  const axes: Axis[] = [];
  const uncertainties: Uncertainty[] = [];
  const strict: Schema = {};
  const generalized: Schema = {};
  const types = typeList(node);
  const observations = node.present - node.nullCount;

  const merge = (sub: FieldSchemas): void => {
    axes.push(...sub.axes);
    uncertainties.push(...sub.uncertainties);
  };

  if (types.length === 0) {
    uncertainties.push({
      path: node.path,
      axis: "domain-unknown",
      observation: `no non-null observations in ${node.present} observation(s)`,
      caveat: "value domain is unknown; no type claim is made",
    });
  } else if (types.length === 1) {
    const [kind, count] = types[0];
    switch (kind) {
      case "integer": {
        strict.type = "integer";
        if (node.numericMin) strict.minimum = node.numericMin.value;
        if (node.numericMax) strict.maximum = node.numericMax.value;
        generalized.type = "number";
        axes.push({
          kind: "integer-vs-number",
          path: node.path,
          rulePath: `${rulePath}/type`,
          detail: `only integers observed (${count}x); "integer" is sample-inductive`,
        });
        uncertainties.push({
          path: node.path,
          axis: "integer-vs-number",
          observation: `all ${count} observed value(s) are integers`,
          caveat: "decimals are NOT ruled out; integer-ness is a sample-inductive guess",
        });
        addRangeAxes(node, rulePath, axes, uncertainties);
        break;
      }
      case "decimal": {
        strict.type = "number";
        generalized.type = "number";
        if (node.numericMin) strict.minimum = node.numericMin.value;
        if (node.numericMax) strict.maximum = node.numericMax.value;
        addRangeAxes(node, rulePath, axes, uncertainties);
        break;
      }
      case "string": {
        strict.type = "string";
        generalized.type = "string";
        if (node.minLengthBound) strict.minLength = node.minLengthBound.value;
        if (node.maxLengthBound) strict.maxLength = node.maxLengthBound.value;
        if (node.minLengthBound || node.maxLengthBound) {
          axes.push({
            kind: "string-length",
            path: node.path,
            rulePath: `${rulePath}/maxLength`,
            detail: `observed lengths [${node.minLengthBound?.value}, ${node.maxLengthBound?.value}] are sample-inductive`,
          });
          uncertainties.push({
            path: node.path,
            axis: "string-length",
            observation: `observed lengths in [${node.minLengthBound?.value}, ${node.maxLengthBound?.value}] over ${count} value(s)`,
            caveat: "shorter or longer strings are NOT ruled out",
          });
        }
        if (!node.stringsTruncated && node.distinctStrings.size <= ENUM_CANDIDATE_CAP) {
          strict.enum = [...node.distinctStrings.keys()].sort();
          // enum applies to the whole value; a nullable field must list null
          if (node.nullCount > 0) strict.enum.push(null);
          axes.push({
            kind: "string-enum",
            path: node.path,
            rulePath: `${rulePath}/enum`,
            detail: `only ${node.distinctStrings.size} distinct value(s) observed; enum is sample-inductive`,
          });
          uncertainties.push({
            path: node.path,
            axis: "string-enum",
            observation: `distinct values observed: ${[...node.distinctStrings.keys()].map((v) => JSON.stringify(v)).join(", ")}`,
            caveat: "unseen values are NOT ruled out; the enum is closed only over the samples",
          });
        }
        break;
      }
      case "boolean": {
        strict.type = "boolean";
        generalized.type = "boolean";
        break;
      }
      case "object": {
        strict.type = "object";
        generalized.type = "object";
        const strictProps: Record<string, Schema> = {};
        const genProps: Record<string, Schema> = {};
        const required: string[] = [];
        for (const [key, child] of [...node.children.entries()].sort(([a], [b]) => a.localeCompare(b))) {
          const sub = inferNode(child, `${rulePath}/properties/${escapePointer(key)}`);
          strictProps[key] = sub.strict;
          genProps[key] = sub.generalized;
          merge(sub);
          if (child.missing === 0) {
            required.push(key);
            axes.push({
              kind: "required",
              path: node.path,
              key,
              rulePath: `${rulePath}/required`,
              detail: `"${key}" present in all ${count} observed object(s); requiring it is sample-inductive`,
            });
            uncertainties.push({
              path: child.path,
              axis: "required",
              observation: `present in all ${count} observed object(s) at ${node.path}`,
              caveat: "future absence is NOT ruled out; required-ness is sample-inductive",
            });
          }
        }
        strict.properties = strictProps;
        generalized.properties = genProps;
        if (required.length > 0) strict.required = required;
        uncertainties.push({
          path: node.path,
          axis: "unseen-properties",
          observation: `observed keys: ${[...node.children.keys()].map((k) => JSON.stringify(k)).join(", ")}`,
          caveat: "properties never observed are UNKNOWN, not forbidden; no additionalProperties:false is emitted",
        });
        break;
      }
      case "array": {
        strict.type = "array";
        generalized.type = "array";
        if (node.items && node.items.present > 0) {
          const sub = inferNode(node.items, `${rulePath}/items`);
          strict.items = sub.strict;
          generalized.items = sub.generalized;
          merge(sub);
        } else {
          uncertainties.push({
            path: `${node.path}[]`,
            axis: "domain-unknown",
            observation: "only empty arrays observed",
            caveat: "element domain is unknown; no items claim is made",
          });
        }
        if (node.minItemsBound) strict.minItems = node.minItemsBound.value;
        if (node.maxItemsBound) strict.maxItems = node.maxItemsBound.value;
        if (node.minItemsBound || node.maxItemsBound) {
          axes.push({
            kind: "array-length",
            path: node.path,
            rulePath: `${rulePath}/maxItems`,
            detail: `observed lengths [${node.minItemsBound?.value}, ${node.maxItemsBound?.value}] are sample-inductive`,
          });
          uncertainties.push({
            path: node.path,
            axis: "array-length",
            observation: `observed lengths in [${node.minItemsBound?.value}, ${node.maxItemsBound?.value}] over ${count} array(s)`,
            caveat: "shorter or longer arrays are NOT ruled out",
          });
        }
        break;
      }
    }
  } else {
    // Mixed types: the union is FORCED by evidence, so both profiles agree.
    const names = new Set<PrimitiveType>();
    for (const [kind] of types) {
      if (kind === "integer" || kind === "decimal") names.add("number");
      else names.add(kind as PrimitiveType);
    }
    const union = [...names].sort();
    strict.type = union.length === 1 ? union[0] : union;
    generalized.type = union.length === 1 ? union[0] : union;
    uncertainties.push({
      path: node.path,
      axis: "mixed-types",
      observation: `observed types: ${types.map(([k, n]) => `${k}(${n}x)`).join(", ")}`,
      caveat: "union is evidence-forced; relative likelihood of each type is unknown",
    });
  }

  if (node.nullCount > 0) {
    addNull(strict);
    addNull(generalized);
  } else if (observations > 0) {
    uncertainties.push({
      path: node.path,
      axis: "nullability-unknown",
      observation: `null never observed in ${node.present} observation(s)`,
      caveat: "nullability is unknown; candidates reject null only because samples did, not by guarantee",
    });
  }

  return { strict, generalized, axes, uncertainties };
}

function addRangeAxes(
  node: EvidenceNode,
  rulePath: string,
  axes: Axis[],
  uncertainties: Uncertainty[],
): void {
  if (!node.numericMin && !node.numericMax) return;
  axes.push({
    kind: "numeric-range",
    path: node.path,
    rulePath: `${rulePath}/maximum`,
    detail: `observed range [${node.numericMin?.value}, ${node.numericMax?.value}] is sample-inductive`,
  });
  uncertainties.push({
    path: node.path,
    axis: "numeric-range",
    observation: `observed range [${node.numericMin?.value}, ${node.numericMax?.value}] (min from ${node.numericMin?.sampleIds.join("/")}, max from ${node.numericMax?.sampleIds.join("/")})`,
    caveat: "values outside the observed range are NOT ruled out",
  });
}

// Number of inductive (not evidence-forced) assumptions in a schema.
export function countInductive(schema: Schema): number {
  let n = 0;
  const t = schema.type;
  if (t === "integer" || (Array.isArray(t) && t.includes("integer"))) n++;
  if (schema.minimum !== undefined) n++;
  if (schema.maximum !== undefined) n++;
  if (schema.minLength !== undefined) n++;
  if (schema.maxLength !== undefined) n++;
  if (schema.minItems !== undefined) n++;
  if (schema.maxItems !== undefined) n++;
  if (schema.enum !== undefined) n++;
  if (schema.required) n += schema.required.length;
  for (const sub of Object.values(schema.properties ?? {})) n += countInductive(sub);
  if (schema.items) n += countInductive(schema.items);
  return n;
}
