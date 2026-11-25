import { test } from "node:test";
import assert from "node:assert/strict";
import { issueCertificate, verifyCertificate } from "../src/certificate.ts";
import { canonicalizeGrammar } from "../src/grammar.ts";
import {
  enumerateGrammarTerms,
  termKey,
  unfoldGraph,
} from "../src/unfold.ts";
import { admit } from "../src/runtime.ts";
import {
  baseConfig,
  ctor,
  graph,
  named,
  oldTable,
  ref,
  tenantGrammar,
} from "./fixtures.ts";
import type { BindingTable } from "../src/types.ts";

test("budget exhaustion yields insufficient and cannot be published", () => {
  const table = oldTable();
  const doc = tenantGrammar(table);
  const issue = issueCertificate({
    oldTable: table,
    newTable: table,
    oldGrammar: doc,
    newGrammar: doc,
    config: baseConfig({ budget: 0 }),
  });
  assert.equal(issue.certificate.verdict, "insufficient");
  assert.equal(issue.publishable, false);
});

test("a tiny budget that completes still produces a subset certificate", () => {
  const table = oldTable();
  const doc = tenantGrammar(table);
  const issue = issueCertificate({
    oldTable: table,
    newTable: table,
    oldGrammar: doc,
    newGrammar: doc,
    config: baseConfig({ budget: 2 }),
  });
  assert.equal(issue.certificate.verdict, "insufficient");
  assert.ok(issue.certificate.spent >= issue.certificate.budget);
});

test("tampered certificates fail independent verification and runtime", () => {
  const table = oldTable();
  const doc = tenantGrammar(table);
  const issue = issueCertificate({
    oldTable: table,
    newTable: table,
    oldGrammar: doc,
    newGrammar: doc,
    config: baseConfig(),
  });
  const grammar = issue.leftGrammar;
  const report = verifyCertificate(
    issue.certificate,
    grammar,
    issue.rightGrammar,
    issue.ranges,
  );
  assert.equal(report.valid, true, report.errors.join("; "));

  const tampered = {
    ...issue.certificate,
    proof: issue.certificate.proof.slice(0, 1),
  };
  const badReport = verifyCertificate(tampered, grammar, issue.rightGrammar, issue.ranges);
  assert.equal(badReport.valid, false);

  const runtime = admit({
    tenant: "tenant-1",
    graph: graph("r", named("r", "System.String")),
    bundles: [
      {
        table,
        grammar,
        certificate: { ...issue.certificate, verdict: "widened" },
        ranges: new Map(),
      },
    ],
  });
  assert.equal(runtime.admitted, false);
});

test("depth-bounded exhaustive unfolding agrees with coinductive admission", () => {
  const table: BindingTable = {
    tableId: "oracle",
    rulesVersion: "admission-rules/1.0.0",
    constructors: [
      ctor("System.String", []),
      ctor("Generic.List", ["out"]),
    ],
  };
  const doc = {
    tenant: "tenant-oracle",
    start: "S",
    nonterminals: {
      S: [
        { constructor: table.constructors[0], children: [] },
        { constructor: table.constructors[1], children: ["S"] },
      ],
    },
  };
  const grammar = canonicalizeGrammar(doc, table);
  const issue = issueCertificate({
    oldTable: table,
    newTable: table,
    oldGrammar: doc,
    newGrammar: doc,
    config: baseConfig(),
  });
  const bundle = {
    table,
    grammar,
    certificate: issue.certificate,
    ranges: new Map(),
  };

  const listGraph = graph(
    "l",
    named("l", "Generic.List", [ref("l")]),
  );
  const depth = 3;
  const unfolded = unfoldGraph(listGraph, depth, grammar);
  const language = new Set(
    enumerateGrammarTerms(grammar, depth).map(termKey),
  );
  assert.equal(
    unfolded.terms.every((term) => language.has(termKey(term))),
    true,
  );

  const accepted = admit({ tenant: "tenant-oracle", graph: listGraph, bundles: [bundle] });
  assert.equal(accepted.admitted, true);

  const foreign = graph(
    "r",
    named("r", "Generic.List", [named("x", "System.DateTime")]),
  );
  const rejected = admit({ tenant: "tenant-oracle", graph: foreign, bundles: [bundle] });
  assert.equal(rejected.admitted, false);
  assert.equal(rejected.rejection?.kind, "unknown-constructor");
});

test("coexisting table releases are tried until a certified bundle matches", () => {
  const old = oldTable();
  const oldDoc = tenantGrammar(old);
  const oldGrammar = canonicalizeGrammar(oldDoc, old);
  const oldIssue = issueCertificate({
    oldTable: old,
    newTable: old,
    oldGrammar: oldDoc,
    newGrammar: oldDoc,
    config: baseConfig(),
  });

  const newTable: typeof old = {
    ...old,
    tableId: "table-new",
    constructors: [
      ...old.constructors,
      ctor("System.DateTime", []),
    ],
  };
  const newDoc = { ...tenantGrammar(newTable), tenant: "tenant-2" };
  newDoc.nonterminals.S.push({
    constructor: newTable.constructors.find((d) => d.typeName === "System.DateTime")!,
    children: [],
  });
  const newGrammar = canonicalizeGrammar(newDoc, newTable);
  const selfIssue = issueCertificate({
    oldTable: newTable,
    newTable: newTable,
    oldGrammar: newDoc,
    newGrammar: newDoc,
    config: baseConfig(),
  });

  const dateGraph = graph("r", named("r", "System.DateTime"));
  const result = admit({
    tenant: "tenant-2",
    graph: dateGraph,
    bundles: [
      { table: old, grammar: oldGrammar, certificate: oldIssue.certificate, ranges: new Map() },
      { table: newTable, grammar: newGrammar, certificate: selfIssue.certificate, ranges: new Map() },
    ],
  });
  assert.equal(result.admitted, true);
  assert.equal(result.tableId, "table-new");
});
