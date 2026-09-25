import { compilePolicy } from "../src/policy.ts";
import type { PolicySpec } from "../src/policy.ts";
import { executeQuery } from "../src/engine.ts";
import type { Row } from "../src/engine.ts";

const spec: PolicySpec = {
  subjectAttributes: [
    { name: "role", type: { kind: "enum", values: ["analyst", "manager", "hr", "admin"] } },
    { name: "clearance", type: { kind: "integer" } },
    { name: "department", type: { kind: "enum", values: ["eng", "sales", "finance"] } },
  ],
  rowAttributes: [
    { name: "classification", type: { kind: "integer" } },
    { name: "region", type: { kind: "enum", values: ["cn", "eu", "us"] } },
  ],
  publicFields: ["id", "name", "department", "salary", "bonus", "ssn", "note"],
  rowVisibleWhen: {
    op: "and",
    all: [
      { op: "int", attribute: "row.classification", cmp: "lte", otherAttribute: "subject.clearance" },
      { op: "in", attribute: "row.region", values: ["cn", "eu"] },
    ],
  },
  maskRules: [
    {
      id: "comp-hr-only",
      when: { op: "not", cond: { op: "in", attribute: "subject.role", values: ["hr", "admin"] } },
      fields: ["salary", "ssn"],
    },
    {
      id: "ssn-needs-clearance-3",
      when: { op: "int", attribute: "subject.clearance", cmp: "lt", value: 3 },
      fields: ["ssn"],
    },
    {
      id: "note-sensitive-rows",
      when: {
        op: "and",
        all: [
          { op: "int", attribute: "row.classification", cmp: "gt", value: 1 },
          { op: "not", cond: { op: "in", attribute: "subject.role", values: ["admin"] } },
        ],
      },
      fields: ["note"],
    },
  ],
};

const policy = compilePolicy(spec);

const employees: readonly Row[] = [
  { id: 1, name: "alice", department: "eng", region: "cn", classification: 1, salary: 18000, bonus: 2000, ssn: "CN-1001", note: "mentor" },
  { id: 2, name: "bob", department: "sales", region: "eu", classification: 2, salary: 15000, bonus: null, ssn: "EU-2002", note: "q3 pip" },
  { id: 3, name: "carol", department: "eng", region: "us", classification: 1, salary: 21000, bonus: 3000, ssn: "US-3003", note: "staff eng" },
  { id: 4, name: "dave", department: "finance", region: "cn", classification: 4, salary: 26000, bonus: 5000, ssn: "CN-4004", note: "payroll" },
  { id: 5, name: "erin", department: "eng", region: "eu", classification: 2, salary: 19000, bonus: 2500, ssn: "EU-5005", note: "oncall" },
];

const subject = { role: "manager", clearance: 2, department: "eng" };

console.log("== subject ==");
console.log(JSON.stringify(subject));
console.log("");

const result = executeQuery(policy, subject, employees, {
  sortBy: { field: "id" },
  aggregates: [
    { fn: "count", as: "visibleCount" },
    { fn: "sum", field: "bonus", as: "totalBonus" },
    { fn: "avg", field: "bonus", as: "avgBonus" },
  ],
});

console.log("== query result (fixed public schema: columns / rows / aggregates) ==");
console.log(JSON.stringify(result, null, 2));
console.log("");

console.log("== null vs masked ==");
console.log("row id=2 bonus (real null):     " + JSON.stringify(result.rows[1]?.bonus));
console.log("row id=1 salary (masked marker): " + JSON.stringify(result.rows[0]?.salary));
console.log("");

console.log("== rejected: sorting by a maskable field ==");
try {
  executeQuery(policy, subject, employees, { sortBy: { field: "salary" } });
} catch (err) {
  console.log((err as Error).name + ": " + (err as Error).message);
}
console.log("");

console.log("== rejected: aggregating a maskable field ==");
try {
  executeQuery(policy, subject, employees, { aggregates: [{ fn: "sum", field: "salary", as: "s" }] });
} catch (err) {
  console.log((err as Error).name + ": " + (err as Error).message);
}
