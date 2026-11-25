import { test } from "node:test";
import assert from "node:assert/strict";
import { issueCertificate } from "../src/certificate.ts";
import { admit } from "../src/runtime.ts";
import { canonicalizeGrammar } from "../src/grammar.ts";
import {
  baseConfig,
  ctor,
  graph,
  named,
  oldTable,
  ref,
  tenantGrammar,
} from "./fixtures.ts";
import type { BindingTable, GrammarDocument, TypeGraph, TypeNode } from "../src/types.ts";

test("the same shared node is checked under each position's own nonterminal", () => {
  const table: BindingTable = {
    tableId: "shared",
    rulesVersion: "admission-rules/1.0.0",
    constructors: [
      ctor("System.String", []),
      ctor("System.Int32", []),
      ctor("Generic.List", ["out"]),
      ctor("Generic.Box", ["invariant"]),
    ],
  };
  const doc: GrammarDocument = {
    tenant: "tenant-shared",
    start: "S",
    nonterminals: {
      S: [
        { constructor: table.constructors[2], children: ["T"] },
        { constructor: table.constructors[3], children: ["U"] },
      ],
      T: [
        { constructor: table.constructors[0], children: [] },
        { constructor: table.constructors[1], children: [] },
      ],
      U: [{ constructor: table.constructors[0], children: [] }],
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

  const shared = named("shared", "System.Int32");
  const okGraph = graph(
    "root",
    named("root", "Generic.List", [ref("shared")]),
    shared,
  );
  const ok = admit({
    tenant: "tenant-shared",
    graph: okGraph,
    bundles: [
      { table, grammar, certificate: issue.certificate, ranges: new Map() },
    ],
  });
  assert.equal(ok.admitted, true);

  const badGraph = graph(
    "root",
    named("root", "Generic.Box", [ref("shared")]),
    shared,
  );
  const bad = admit({
    tenant: "tenant-shared",
    graph: badGraph,
    bundles: [
      { table, grammar, certificate: issue.certificate, ranges: new Map() },
    ],
  });
  assert.equal(bad.admitted, false);
  assert.equal(bad.rejection?.kind, "no-matching-production");
  assert.deepEqual(bad.rejection?.path, ["args[0]"]);
});

test("input deeper than the JS call stack is handled iteratively", () => {
  const depth = 50_000;
  const nodes: Record<string, TypeNode> = {};
  for (let index = depth; index >= 0; index--) {
    if (index === depth) {
      nodes[`n${index}`] = named(`n${index}`, "System.String");
    } else {
      nodes[`n${index}`] = named(`n${index}`, "Generic.List", [
        ref(`n${index + 1}`),
      ]);
    }
  }
  const deepGraph: TypeGraph = { root: "n0", nodes };
  const table = oldTable();
  const grammar = canonicalizeGrammar(tenantGrammar(table), table);
  const issue = issueCertificate({
    oldTable: table,
    newTable: table,
    oldGrammar: tenantGrammar(table),
    newGrammar: tenantGrammar(table),
    config: baseConfig(),
  });
  const result = admit({
    tenant: "tenant-1",
    graph: deepGraph,
    bundles: [
      { table, grammar, certificate: issue.certificate, ranges: new Map() },
    ],
  });
  assert.equal(result.admitted, true);
});

test("rejection on a deep graph reports the precise first position", () => {
  const depth = 5_000;
  const nodes: Record<string, TypeNode> = {};
  for (let index = depth; index >= 0; index--) {
    if (index === depth) {
      nodes[`n${index}`] = named(`n${index}`, "System.Int32");
    } else {
      nodes[`n${index}`] = named(`n${index}`, "Generic.List", [
        ref(`n${index + 1}`),
      ]);
    }
  }
  const table = oldTable();
  const grammar = canonicalizeGrammar(tenantGrammar(table), table);
  const issue = issueCertificate({
    oldTable: table,
    newTable: table,
    oldGrammar: tenantGrammar(table),
    newGrammar: tenantGrammar(table),
    config: baseConfig(),
  });
  const result = admit({
    tenant: "tenant-1",
    graph: { root: "n0", nodes },
    bundles: [
      { table, grammar, certificate: issue.certificate, ranges: new Map() },
    ],
  });
  assert.equal(result.admitted, false);
  assert.equal(result.rejection?.path.length, depth);
  assert.equal(result.rejection?.nodeId, `n${depth}`);
});
