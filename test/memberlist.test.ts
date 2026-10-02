import assert from "node:assert/strict";
import { test } from "node:test";
import { MemberList } from "../src/memberlist.ts";

test("alive refutes suspect at the same incarnation", () => {
  const m = new MemberList("self");
  assert.equal(m.apply({ type: "join", nodeId: "n1", incarnation: 2 }), true);
  assert.equal(m.apply({ type: "suspect", nodeId: "n1", incarnation: 2 }), true);
  assert.equal(m.statusOf("n1"), "suspect");
  assert.equal(m.apply({ type: "alive", nodeId: "n1", incarnation: 2 }), true);
  assert.equal(m.statusOf("n1"), "alive");
});

test("stale events are ignored", () => {
  const m = new MemberList("self");
  m.apply({ type: "alive", nodeId: "n1", incarnation: 5 });
  assert.equal(m.apply({ type: "suspect", nodeId: "n1", incarnation: 4 }), false);
  assert.equal(m.apply({ type: "join", nodeId: "n1", incarnation: 0 }), false);
  assert.equal(m.statusOf("n1"), "alive");
});

test("leave is terminal and overrides alive/suspect", () => {
  const m = new MemberList("self");
  m.apply({ type: "join", nodeId: "n1", incarnation: 1 });
  m.apply({ type: "suspect", nodeId: "n1", incarnation: 1 });
  assert.equal(m.apply({ type: "leave", nodeId: "n1", incarnation: 1 }), true);
  assert.equal(m.statusOf("n1"), "left");
  assert.equal(m.apply({ type: "alive", nodeId: "n1", incarnation: 9 }), false);
});

test("suspect overrides alive only at same or higher incarnation", () => {
  const m = new MemberList("self");
  m.apply({ type: "alive", nodeId: "n1", incarnation: 3 });
  assert.equal(m.apply({ type: "suspect", nodeId: "n1", incarnation: 2 }), false);
  assert.equal(m.apply({ type: "suspect", nodeId: "n1", incarnation: 3 }), true);
});
