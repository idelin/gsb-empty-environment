import { executeQuery, toPublicJSON, type Policy, type Row } from "./engine.ts";

// Explicit attributes only: dept (finite set), clearance (integer), active (bool).
const rows: Row[] = [
  { attrs: { dept: "eng",  clearance: 3, active: true  }, data: { name: "Ada",   salary: 185000, ssn: "111-22-3333", note: null } },
  { attrs: { dept: "eng",  clearance: 1, active: true  }, data: { name: "Bjarne",salary: 142000, ssn: "444-55-6666", note: "visa" } },
  { attrs: { dept: "fin",  clearance: 2, active: true  }, data: { name: "Cleo",  salary: 160000, ssn: "777-88-9999", note: null } },
  { attrs: { dept: "fin",  clearance: 0, active: false }, data: { name: "Dan",   salary: 98000,  ssn: "123-45-6789", note: "leave" } }, // hidden: inactive
  { attrs: { dept: "ops",  clearance: 5, active: true  }, data: { name: "Eve",   salary: 121000, ssn: "000-11-2222", note: null } },   // hidden: dept not in set
];

const policy: Policy = {
  fields: ["name", "salary", "ssn", "note"],
  // Visible rows: active AND dept in a finite set.
  row: {
    op: "and",
    args: [
      { op: "in", attr: "dept", values: ["eng", "fin"] },
      { op: "in", attr: "active", values: [true] },
    ],
  },
  reveal: {
    name: { op: "const", value: true },
    // salary revealed only for clearance >= 2 (integer condition).
    salary: { op: "int", attr: "clearance", cmp: "gte", value: 2 },
    // ssn requires clearance >= 2 AND dept = fin (boolean composition).
    ssn: {
      op: "and",
      args: [
        { op: "int", attr: "clearance", cmp: "gte", value: 2 },
        { op: "in", attr: "dept", values: ["fin"] },
      ],
    },
    // note has no reveal condition -> deny by default (always masked).
  },
};

const output = executeQuery(
  rows,
  policy,
  {
    sort: [{ field: "salary", dir: "desc" }],
    aggregates: [
      { fn: "count", as: "headcount" },
      { fn: "sum", field: "salary", as: "payroll" },
      { fn: "avg", field: "salary", as: "avgSalary" },
    ],
  },
  ["dept", "clearance", "active"],
);

console.log(toPublicJSON(output));
