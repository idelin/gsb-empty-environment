import { test } from "node:test";
import assert from "node:assert/strict";

import {
  FiniteStateModel,
  UnsafeDescriptionError,
  createHost,
} from "../src/index.ts";

test("finite-state bisimulation is coinductive over cycles and strict on labels", () => {
  const model = new FiniteStateModel([
    { id: "s1", observation: "A", transitions: [{ label: "x", to: "s2" }] },
    { id: "s2", observation: "B", transitions: [{ label: "y", to: "s1" }] },
    { id: "s3", observation: "A", transitions: [{ label: "x", to: "s4" }] },
    { id: "s4", observation: "B", transitions: [{ label: "y", to: "s3" }] },
    { id: "s5", observation: "A", transitions: [{ label: "x", to: "s6" }] },
    { id: "s6", observation: "B", transitions: [{ label: "z", to: "s5" }] },
    { id: "s7", observation: "A", transitions: [{ label: "x", to: "s7" }] },
  ]);
  const result = model.bisimulation();

  assert.equal(result.isBisimilar("s1", "s3"), true);
  assert.equal(result.isBisimilar("s1", "s5"), false);
  assert.equal(result.isBisimilar("s5", "s7"), false);
  assert.equal(result.isBisimilar("s1", "s7"), false);
  assert.equal(model.hasPath("s1", "s3", "x"), false);
  assert.equal(model.hasPath("s1", "s2", "x"), true);
  assert.deepEqual(
    [...model.reachableFrom("s1")].sort(),
    ["s1", "s2"],
  );
});

test("host round trip matches the finite-state policy relation", () => {
  const graph = {
    nodes: {
      root: { properties: { name: { value: "root" } } },
    },
  };
  const policy = {
    entry: "a",
    nodes: {
      a: { domain: "A" },
      b: { domain: "B" },
      c: { domain: "C" },
      a2: { domain: "A" },
    },
    edges: [
      { id: "ab", from: "a", to: "b", action: "pass" },
      { id: "bc", from: "b", to: "c", action: "pass" },
      { id: "ca", from: "c", to: "a2", action: "pass" },
      { id: "a2b", from: "a2", to: "b", action: "pass" },
    ],
  };
  const host = createHost({ graph, policy });

  const expected = new FiniteStateModel([
    { id: "A0", observation: "A", transitions: [{ label: "pass", to: "B0" }] },
    { id: "B0", observation: "B", transitions: [{ label: "pass", to: "C0" }] },
    { id: "C0", observation: "C", transitions: [{ label: "pass", to: "A2" }] },
    { id: "A2", observation: "A", transitions: [{ label: "pass", to: "B0" }] },
  ]);
  assert.equal(expected.bisimulation().isBisimilar("A0", "A2"), true);
  assert.equal(
    host.policy.isBisimilar(
      host.policy.configOf("a"),
      host.policy.configOf("a2"),
    ),
    true,
  );

  const actual = host.policyModel().bisimulation();
  assert.equal(
    actual.isBisimilar(
      host.policy.configId(host.policy.configOf("a")),
      host.policy.configId(host.policy.configOf("a2")),
    ),
    true,
  );
  assert.equal(host.snapshot().policy.classCount, actual.classes.length);

  const v0 = host.view("root");
  const roundTrip = host.pass(host.pass(host.pass(v0, "ab"), "bc"), "ca");
  assert.equal(roundTrip, v0);
  assert.equal(host.isSameIdentity(roundTrip, v0), true);
});

test("diamond partial revocation is visible in the finite-state reachability model", () => {
  const graph = {
    nodes: {
      doc: { properties: { title: { value: "shared" } } },
    },
  };
  const policy = {
    entry: "s",
    nodes: {
      s: { domain: "A" },
      l: { domain: "B" },
      r: { domain: "C" },
      tL: { domain: "D" },
      tR: { domain: "D" },
    },
    edges: [
      { id: "eL", from: "s", to: "l", kind: "grant", action: "open" },
      { id: "eL2", from: "l", to: "tL", kind: "grant", action: "open" },
      { id: "eR", from: "s", to: "r", kind: "grant", action: "open" },
      { id: "eR2", from: "r", to: "tR", kind: "grant", action: "open" },
    ],
  };
  const host = createHost({ graph, policy });
  const root = host.view("doc");
  const left = host.pass(host.pass(root, "eL"), "eL2");
  const right = host.pass(host.pass(root, "eR"), "eR2");

  const expected = new FiniteStateModel([
    {
      id: "S",
      observation: "A",
      transitions: [
        { label: "open", to: "L" },
        { label: "open", to: "R" },
      ],
    },
    { id: "L", observation: "B", transitions: [{ label: "open", to: "TL" }] },
    { id: "R", observation: "C", transitions: [{ label: "open", to: "TR" }] },
    { id: "TL", observation: "D" },
    { id: "TR", observation: "D" },
  ]);
  assert.equal(expected.bisimulation().isBisimilar("TL", "TR"), true);
  assert.equal(host.isSameIdentity(left, right), true);
  assert.notEqual(left, right);

  host.revoke("eL2");
  assert.equal(host.isRevoked(left), true);
  assert.equal(host.isRevoked(right), false);
  assert.equal(right.title, "shared");

  const afterRevocation = new FiniteStateModel([
    {
      id: "S",
      observation: "A",
      transitions: [
        { label: "open", to: "L" },
        { label: "open", to: "R" },
      ],
    },
    { id: "L", observation: "B", transitions: [] },
    { id: "R", observation: "C", transitions: [{ label: "open", to: "TR" }] },
    { id: "TL", observation: "D" },
    { id: "TR", observation: "D" },
  ]);
  const reachable = afterRevocation.reachableFrom("S");
  assert.equal(reachable.has("TL"), false);
  assert.equal(reachable.has("TR"), true);
});

test("non-reflective non-execution constraints are modelled without an execute edge", () => {
  let executed = false;
  const graph = {
    nodes: {
      root: {
        properties: {
          dangerous: {
            value: () => {
              executed = true;
              return 1;
            },
          },
        },
      },
    },
  };

  const constraintModel = new FiniteStateModel([
    { id: "received", observation: "spec", transitions: [{ label: "validate", to: "checked" }] },
    { id: "checked", observation: "immutable-data", transitions: [] },
    { id: "execute", observation: "forbidden", transitions: [] },
  ]);
  assert.equal(constraintModel.hasPath("received", "execute"), false);
  assert.deepEqual([...constraintModel.reachableFrom("received")].sort(), [
    "checked",
    "received",
  ]);

  assert.throws(() => createHost({ graph }), (error) => {
    return error instanceof UnsafeDescriptionError;
  });
  assert.equal(executed, false);
});
