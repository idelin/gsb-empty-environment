/**
 * Every issue carries BOTH the instance path (where in the data) and the
 * rule path (where in the schema/rule document). Related paths (e.g. the
 * other endpoint of an order violation) are listed under `related`.
 */

export type IssueKind =
  | "unsupported-feature" // schema/rule uses something outside the promised subset
  | "invalid-schema" // the schema itself is malformed (e.g. min > max)
  | "invalid-rule" // a rule document is malformed
  | "uncovered-path" // a rule references a path the schema does not describe
  | "type" // instance value has the wrong type
  | "range" // int outside its declared [min, max]
  | "missing-required" // required (or conditionally required) field is absent
  | "relation" // equality / order / sum constraint violated
  | "unknown-condition" // condition cannot be decided; NOT treated as false
  | "unknown-operand"; // operand missing; the relation itself is unknown

export interface RelatedPath {
  readonly instancePath?: string;
  readonly rulePath?: string;
  readonly note?: string;
}

export interface Issue {
  readonly kind: IssueKind;
  readonly instancePath: string;
  readonly rulePath: string;
  readonly message: string;
  readonly related: readonly RelatedPath[];
}

export function makeIssue(init: {
  readonly kind: IssueKind;
  readonly instancePath?: string;
  readonly rulePath: string;
  readonly message: string;
  readonly related?: readonly RelatedPath[];
}): Issue {
  return {
    kind: init.kind,
    instancePath: init.instancePath ?? "",
    rulePath: init.rulePath,
    message: init.message,
    related: init.related ?? [],
  };
}

export function formatIssue(issue: Issue): string {
  const parts = [
    `[${issue.kind}]`,
    issue.message,
    `instance=${issue.instancePath === "" ? "(none)" : issue.instancePath}`,
    `rule=${issue.rulePath}`,
  ];
  for (const rel of issue.related) {
    const bits = [
      rel.instancePath === undefined ? null : `instance=${rel.instancePath}`,
      rel.rulePath === undefined ? null : `rule=${rel.rulePath}`,
      rel.note === undefined ? null : rel.note,
    ].filter((bit) => bit !== null);
    parts.push(`related(${bits.join(", ")})`);
  }
  return parts.join(" ");
}
