import { test } from "node:test";
import assert from "node:assert/strict";
import { DeploymentError, validateDeployment } from "../src/deployment.ts";
import { issueCertificate } from "../src/certificate.ts";
import { admit } from "../src/runtime.ts";
import { canonicalizeGrammar } from "../src/grammar.ts";
import {
  baseConfig,
  ctor,
  graph,
  named,
  oldTable,
  PK_A,
  tenantGrammar,
} from "./fixtures.ts";
import type { BindingTable, GrammarDocument } from "../src/types.ts";

function docFor(table: BindingTable): GrammarDocument {
  const find = (typeName: string) =>
    table.constructors.find((descriptor) => descriptor.typeName === typeName)!;
  return {
    tenant: "tenant-1",
    start: "S",
    nonterminals: {
      S: [
        { constructor: find("System.String"), children: [] },
        { constructor: find("Generic.List"), children: ["S"] },
        { constructor: find("Generic.Box"), children: ["S"] },
        { constructor: find("Generic.Func"), children: ["S", "S"] },
      ],
      I: [{ constructor: find("System.Int32"), children: [] }],
    },
  };
}

function varianceTables(): { old: BindingTable; widened: BindingTable } {
  const old = oldTable();
  const widened: BindingTable = {
    tableId: "table-new",
    rulesVersion: old.rulesVersion,
    constructors: old.constructors.map((descriptor) =>
      descriptor.typeName === "Generic.Box"
        ? { ...descriptor, variances: ["out"] }
        : descriptor,
    ),
  };
  return { old, widened };
}

test("implicit covariance relaxation is rejected at configuration time", () => {
  const { old, widened } = varianceTables();
  assert.throws(
    () =>
      validateDeployment(baseConfig(), old, widened),
    (error: unknown) =>
      error instanceof DeploymentError &&
      error.message.includes("implicit variance change"),
  );
});

test("explicit variance mark yields a widening verdict, never a subset release", () => {
  const { old, widened } = varianceTables();
  const issue = issueCertificate({
    oldTable: old,
    newTable: widened,
    oldGrammar: docFor(old),
    newGrammar: docFor(widened),
    config: baseConfig({
      varianceMarks: [
        {
          ctor: { assemblyPublicKey: PK_A, typeName: "Generic.Box" },
          slot: 0,
          variance: "out",
        },
      ],
    }),
  });
  assert.equal(issue.certificate.verdict, "widened");
  assert.equal(issue.publishable, false);
  assert.ok(
    issue.certificate.witnesses.some(
      (witness) => witness.kind === "unmatched-production" || witness.kind === "empty-language",
    ),
  );
});

test("covariant and invariant slots admit different sets at runtime", () => {
  const table: BindingTable = {
    tableId: "table-slots",
    rulesVersion: "admission-rules/1.0.0",
    constructors: [
      ctor("System.String", []),
      ctor("System.Int32", []),
      ctor("Generic.List", ["out"]),
      ctor("Generic.Box", ["invariant"]),
    ],
  };
  const doc: GrammarDocument = {
    tenant: "tenant-variance",
    start: "S",
    nonterminals: {
      S: [
        { constructor: table.constructors[0], children: [] },
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
  assert.equal(issue.publishable, true);
  const bundle = { table, grammar, certificate: issue.certificate, ranges: new Map() };

  const listOfInt = graph(
    "root",
    named("root", "Generic.List", [named("n", "System.Int32")]),
  );
  assert.equal(admit({ tenant: "tenant-variance", graph: listOfInt, bundles: [bundle] }).admitted, true);

  const boxOfInt = graph(
    "root",
    named("root", "Generic.Box", [named("n", "System.Int32")]),
  );
  const boxResult = admit({ tenant: "tenant-variance", graph: boxOfInt, bundles: [bundle] });
  assert.equal(boxResult.admitted, false);
  assert.equal(boxResult.rejection?.kind, "no-matching-production");
  assert.deepEqual(boxResult.rejection?.path, ["args[0]"]);
});

test("contravariant slot flips the inclusion obligation", () => {
  const table: BindingTable = {
    tableId: "table-func",
    rulesVersion: "admission-rules/1.0.0",
    constructors: [
      ctor("System.String", []),
      ctor("System.Int32", []),
      ctor("Generic.Func", ["in", "out"]),
    ],
  };
  const doc: GrammarDocument = {
    tenant: "tenant-func",
    start: "S",
    nonterminals: {
      S: [{ constructor: table.constructors[2], children: ["Arg", "Ret"] }],
      Arg: [{ constructor: table.constructors[0], children: [] }],
      Ret: [{ constructor: table.constructors[1], children: [] }],
    },
  };
  const issue = issueCertificate({
    oldTable: table,
    newTable: table,
    oldGrammar: doc,
    newGrammar: doc,
    config: baseConfig(),
  });
  assert.equal(issue.certificate.verdict, "subset");
  const obligations = issue.certificate.proof.filter(
    (obligation) => obligation.direction === "backward",
  );
  assert.ok(obligations.length > 0, "contravariance must add backward obligations");
});
