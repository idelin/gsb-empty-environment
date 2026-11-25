import { test } from "node:test";
import assert from "node:assert/strict";
import {
  DeploymentError,
  assertRangesBackedByOldTable,
  parseVersionRange,
  rangeContains,
} from "../src/deployment.ts";
import { issueCertificate, verifyCertificate } from "../src/certificate.ts";
import { mapNewTable } from "../src/deployment.ts";
import { canonicalizeGrammar } from "../src/grammar.ts";
import { ctorId } from "../src/canonical.ts";
import { admit } from "../src/runtime.ts";
import {
  baseConfig,
  ctor,
  graph,
  named,
  oldTable,
  PK_A,
  PK_B,
  tenantGrammar,
} from "./fixtures.ts";
import type { BindingTable } from "../src/types.ts";

test("same type name under different public keys are distinct constructors", () => {
  const old = oldTable();
  const doc = tenantGrammar(old);
  const wrongKey = graph(
    "root",
    named("root", "Ambiguous.Type", [], {
      assemblyPublicKey: PK_A,
      assemblyVersion: "1.0.0",
    }),
  );
  const grammar = canonicalizeGrammar(doc, old);
  const issue = issueCertificate({
    oldTable: old,
    newTable: old,
    oldGrammar: doc,
    newGrammar: doc,
    config: baseConfig(),
  });
  const result = admit({
    tenant: "tenant-1",
    graph: wrongKey,
    bundles: [
      { table: old, grammar, certificate: issue.certificate, ranges: new Map() },
    ],
  });
  assert.equal(result.admitted, false);
  assert.equal(result.rejection?.kind, "unknown-constructor");
});

test("rename maps a new constructor back onto the old permitted identity", () => {
  const old = oldTable();
  const newTable: BindingTable = {
    tableId: "table-renamed",
    rulesVersion: old.rulesVersion,
    constructors: old.constructors.map((descriptor) =>
      descriptor.typeName === "System.String"
        ? { ...descriptor, typeName: "System.Text" }
        : descriptor,
    ),
  };
  const newDoc = (() => {
    const find = (typeName: string) =>
      newTable.constructors.find((d) => d.typeName === typeName)!;
    return {
      tenant: "tenant-1",
      start: "S",
      nonterminals: {
        S: [
          { constructor: find("System.Text"), children: [] },
          { constructor: find("Generic.List"), children: ["S"] },
          { constructor: find("Generic.Box"), children: ["S"] },
          { constructor: find("Generic.Func"), children: ["S", "S"] },
        ],
        I: [{ constructor: find("System.Int32"), children: [] }],
      },
    };
  })();
  const config = baseConfig({
    renames: [
      {
        from: { assemblyPublicKey: PK_A, typeName: "System.Text" },
        to: { assemblyPublicKey: PK_A, typeName: "System.String" },
      },
    ],
  });
  const issue = issueCertificate({
    oldTable: old,
    newTable,
    oldGrammar: tenantGrammar(old),
    newGrammar: newDoc,
    config,
  });
  assert.equal(issue.certificate.verdict, "subset");
  assert.equal(issue.publishable, true);

  const report = verifyCertificate(
    issue.certificate,
    issue.leftGrammar,
    issue.rightGrammar,
    issue.ranges,
  );
  assert.deepEqual(report.errors, []);
});

test("parameter permutation is applied before variance comparison", () => {
  const old: BindingTable = {
    tableId: "old-pair",
    rulesVersion: "admission-rules/1.0.0",
    constructors: [ctor("System.String", []), ctor("Generic.Pair", ["out", "out"])],
  };
  const swapped: BindingTable = {
    tableId: "new-pair",
    rulesVersion: old.rulesVersion,
    constructors: [
      ctor("System.String", []),
      ctor("Generic.Pair", ["out", "out"]),
    ],
  };
  const doc = {
    tenant: "tenant-pair",
    start: "S",
    nonterminals: {
      S: [{ constructor: old.constructors[1], children: ["S", "S"] }],
    },
  };
  const config = baseConfig({
    permutations: [
      {
        ctor: { assemblyPublicKey: PK_A, typeName: "Generic.Pair" },
        mapping: [1, 0],
      },
    ],
  });
  const issue = issueCertificate({
    oldTable: old,
    newTable: swapped,
    oldGrammar: doc,
    newGrammar: doc,
    config,
  });
  assert.equal(issue.certificate.verdict, "subset");
});

test("version range may only contain old permitted exact versions", () => {
  const range = parseVersionRange("1.0.0");
  assert.equal(rangeContains(range, "1.0.0"), true);
  assert.equal(rangeContains(range, "1.1.0"), false);

  const old = oldTable();
  const newTable: BindingTable = {
    tableId: "table-versioned",
    rulesVersion: old.rulesVersion,
    constructors: old.constructors.map((descriptor) =>
      descriptor.typeName === "System.String"
        ? { ...descriptor, assemblyVersion: "1.1.0" }
        : descriptor,
    ),
  };
  const mapped = mapNewTable(
    baseConfig({
      versionRanges: [
        {
          ctor: { assemblyPublicKey: PK_A, typeName: "System.String" },
          range: { exact: ["1.1.0"] },
        },
      ],
    }),
    old,
    newTable,
  );
  assert.throws(
    () => assertRangesBackedByOldTable(mapped, old),
    /never permitted/,
  );

  const backed = mapNewTable(
    baseConfig({
      versionRanges: [
        {
          ctor: { assemblyPublicKey: PK_A, typeName: "System.String" },
          range: { exact: ["1.0.0"] },
        },
      ],
    }),
    old,
    newTable,
  );
  assert.doesNotThrow(() => assertRangesBackedByOldTable(backed, old));
});

test("a genuinely new constructor widens the old permit and blocks release", () => {
  const old = oldTable();
  const newTable: BindingTable = {
    tableId: "table-extra",
    rulesVersion: old.rulesVersion,
    constructors: [
      ...old.constructors,
      ctor("System.Guid", []),
    ],
  };
  const newDoc = {
    tenant: "tenant-1",
    start: "S",
    nonterminals: {
      ...tenantGrammar(newTable).nonterminals,
    },
  };
  newDoc.nonterminals.S.push({
    constructor: newTable.constructors.find((d) => d.typeName === "System.Guid")!,
    children: [],
  });
  const issue = issueCertificate({
    oldTable: old,
    newTable,
    oldGrammar: tenantGrammar(old),
    newGrammar: newDoc,
    config: baseConfig(),
  });
  assert.equal(issue.certificate.verdict, "widened");
  assert.equal(issue.publishable, false);

  const guidGraph = graph("root", named("root", "System.Guid"));
  const grammar = canonicalizeGrammar(tenantGrammar(old), old);
  const result = admit({
    tenant: "tenant-1",
    graph: guidGraph,
    bundles: [
      { table: old, grammar, certificate: issue.certificate, ranges: new Map() },
    ],
  });
  assert.equal(result.admitted, false);
});

test("ctorId distinguishes public key, version and name", () => {
  const a = ctorId(ctor("X", [], { assemblyPublicKey: PK_A, assemblyVersion: "1.0.0" }));
  const b = ctorId(ctor("X", [], { assemblyPublicKey: PK_B, assemblyVersion: "1.0.0" }));
  const c = ctorId(ctor("X", [], { assemblyPublicKey: PK_A, assemblyVersion: "2.0.0" }));
  assert.notEqual(a, b);
  assert.notEqual(a, c);
});
