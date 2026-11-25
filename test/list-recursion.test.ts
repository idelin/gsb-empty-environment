import { test } from "node:test";
import assert from "node:assert/strict";
import { admit } from "../src/runtime.ts";
import { canonicalizeGrammar } from "../src/grammar.ts";
import { issueCertificate } from "../src/certificate.ts";
import { unfoldGraph, enumerateGrammarTerms, termKey } from "../src/unfold.ts";
import {
  array,
  baseConfig,
  ctor,
  graph,
  named,
  oldTable,
  ref,
  tenantGrammar,
} from "./fixtures.ts";
import type { TypeGraph } from "../src/types.ts";

function listGrammarTable() {
  const table = oldTable();
  return table;
}

test("guarded recursive List is admitted and every finite unfolding is accepted", () => {
  const table = listGrammarTable();
  const grammarDoc = tenantGrammar(table);
  const grammar = canonicalizeGrammar(grammarDoc, table);
  const issue = issueCertificate({
    oldTable: table,
    newTable: table,
    oldGrammar: grammarDoc,
    newGrammar: grammarDoc,
    config: baseConfig(),
  });
  assert.equal(issue.publishable, true);
  assert.equal(issue.certificate.verdict, "subset");

  const listGraph = graph(
    "l0",
    named("l0", "Generic.List", [
      { kind: "ref", target: "l1" },
    ]),
    named("l1", "System.String", []),
  );
  const result = admit({
    tenant: "tenant-1",
    graph: listGraph,
    bundles: [
      {
        table,
        grammar,
        certificate: issue.certificate,
        ranges: new Map(),
      },
    ],
  });
  assert.equal(result.admitted, true, JSON.stringify(result.rejection));

  const depth = 3;
  const unfolded = unfoldGraph(listGraph, depth, grammar);
  const grammarTerms = new Set(
    enumerateGrammarTerms(grammar, depth).map(termKey),
  );
  assert.ok(unfolded.terms.length > 0);
  for (const term of unfolded.terms) {
    assert.ok(
      grammarTerms.has(termKey(term)),
      `unfolding ${termKey(term)} must belong to the certified language`,
    );
  }

  const cyclic: TypeGraph = {
    root: "cons",
    nodes: {
      cons: {
        kind: "named",
        id: "cons",
        assemblyPublicKey: "pk-alpha",
        assemblyVersion: "1.0.0",
        typeName: "Generic.List",
        args: [{ kind: "ref", target: "cons" }],
      },
    },
  };
  const cyclicAdmission = admit({
    tenant: "tenant-1",
    graph: cyclic,
    bundles: [
      { table, grammar, certificate: issue.certificate, ranges: new Map() },
    ],
  });
  assert.equal(cyclicAdmission.admitted, true);
  const cyclicUnfold = unfoldGraph(cyclic, depth, grammar);
  assert.equal(cyclicUnfold.terms.length, 0, "pure cyclic graph has no finite term");
});

test("bare alias ring is illegal before any language matching", () => {
  const table = oldTable();
  const grammar = canonicalizeGrammar(tenantGrammar(table), table);
  const issue = issueCertificate({
    oldTable: table,
    newTable: table,
    oldGrammar: tenantGrammar(table),
    newGrammar: tenantGrammar(table),
    config: baseConfig(),
  });
  const aliasGraph = graph("a", named("a", "System.String", []));
  aliasGraph.nodes.alias1 = ref("alias2");
  aliasGraph.nodes.alias2 = ref("alias1");
  aliasGraph.root = "alias1";

  const unfolded = unfoldGraph(aliasGraph, 3);
  assert.ok(unfolded.rejectedBareAlias);

  const result = admit({
    tenant: "tenant-1",
    graph: aliasGraph,
    bundles: [
      { table, grammar, certificate: issue.certificate, ranges: new Map() },
    ],
  });
  assert.equal(result.admitted, false);
  assert.equal(result.rejection?.kind, "bare-alias-cycle");
});

test("arrays of rank 1 and 2 are synthetic covariant containers", () => {
  const table = oldTable();
  const doc = tenantGrammar(table);
  doc.nonterminals.S.push({
    constructor: {
      assemblyPublicKey: "",
      assemblyVersion: "",
      typeName: "array:1",
      arity: 1,
      variances: ["out"],
      array: true,
      arrayRank: 1,
    },
    children: ["S"],
  });
  doc.nonterminals.S.push({
    constructor: {
      assemblyPublicKey: "",
      assemblyVersion: "",
      typeName: "array:2",
      arity: 1,
      variances: ["out"],
      array: true,
      arrayRank: 2,
    },
    children: ["S"],
  });
  const grammar = canonicalizeGrammar(doc, table);
  const issue = issueCertificate({
    oldTable: table,
    newTable: table,
    oldGrammar: doc,
    newGrammar: doc,
    config: baseConfig(),
  });
  const matrix = graph(
    "m",
    array("m", array("row", named("cell", "System.String")), 2),
  );
  const result = admit({
    tenant: "tenant-1",
    graph: matrix,
    bundles: [
      { table, grammar, certificate: issue.certificate, ranges: new Map() },
    ],
  });
  assert.equal(result.admitted, true, JSON.stringify(result.rejection));
});
