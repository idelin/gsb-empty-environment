import { compilePolicy } from "../src/policy.ts";
import type { CompiledPolicy, PolicySpec } from "../src/policy.ts";
import type { Row } from "../src/engine.ts";

export const SPEC: PolicySpec = {
  subjectAttributes: [
    { name: "role", type: { kind: "enum", values: ["analyst", "hr", "admin"] } },
    { name: "clearance", type: { kind: "integer" } },
  ],
  rowAttributes: [
    { name: "classification", type: { kind: "integer" } },
    { name: "region", type: { kind: "enum", values: ["cn", "eu", "us"] } },
  ],
  publicFields: ["id", "name", "region", "salary", "bonus", "note"],
  rowVisibleWhen: {
    op: "and",
    all: [
      { op: "int", attribute: "row.classification", cmp: "lte", otherAttribute: "subject.clearance" },
      { op: "in", attribute: "row.region", values: ["cn", "eu"] },
    ],
  },
  maskRules: [
    {
      id: "salary-for-hr-only",
      when: { op: "not", cond: { op: "in", attribute: "subject.role", values: ["hr", "admin"] } },
      fields: ["salary"],
    },
    {
      id: "note-for-low-clearance",
      when: { op: "int", attribute: "row.classification", cmp: "gt", value: 1 },
      fields: ["note"],
    },
  ],
};

export function makePolicy(): CompiledPolicy {
  return compilePolicy(SPEC);
}

export const ANALYST = { role: "analyst", clearance: 2 };
export const HR = { role: "hr", clearance: 3 };

export const TABLE: readonly Row[] = [
  { id: 1, name: "alice", region: "cn", classification: 1, salary: 100, bonus: 10, note: "n1" },
  { id: 2, name: "bob", region: "eu", classification: 2, salary: 200, bonus: null, note: "n2" },
  { id: 3, name: "carol", region: "us", classification: 1, salary: 300, bonus: 30, note: "secret-us" },
  { id: 4, name: "dave", region: "cn", classification: 5, salary: 400, bonus: 40, note: "secret-high" },
  { id: 5, name: "erin", region: "eu", classification: 2, salary: 500, bonus: 50, note: "n5" },
];
