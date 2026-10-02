import assert from "node:assert/strict";
import { describe, it } from "node:test";
import { TemporalDelegationKernel } from "../src/kernel.ts";

const INF = Number.POSITIVE_INFINITY;

function kernelWithRole() {
  const kernel = new TemporalDelegationKernel();
  kernel.registerRole({ id: "R", permissions: new Set(["doc:read", "doc:write"]) });
  return kernel;
}

describe("acceptance scenario: early revocation propagates down the delegation chain", () => {
  // A holds R; A delegates R to B over business [10, 50);
  // B re-delegates R to C over business [20, 60);
  // A revokes A->B effective at business 25, known at audit 30.
  function build() {
    const kernel = kernelWithRole();
    kernel.grantRole("A", "R", 0, INF, 0);
    kernel.delegate("A", "B", "R", 10, 50, 10);
    kernel.delegate("B", "C", "R", 20, 60, 20);
    kernel.revokeDelegation("A", "B", "R", 25, 30);
    return kernel;
  }

  it("C is denied at business 28 when queried at audit 35 (revocation known)", () => {
    const kernel = build();
    assert.equal(kernel.hasRole("C", "R", 28, 35), false);
    assert.deepEqual(kernel.effectivePermissions("C", 28, 35), []);
  });

  it("C is allowed at business 28 when queried at audit 25 (revocation not yet known)", () => {
    const kernel = build();
    assert.equal(kernel.hasRole("C", "R", 28, 25), true);
    assert.deepEqual(kernel.effectivePermissions("C", 28, 25), ["doc:read", "doc:write"]);
  });

  it("C is still allowed at business 24 even at audit 35 (revocation effective from 25)", () => {
    const kernel = build();
    assert.equal(kernel.hasRole("C", "R", 24, 35), true);
  });

  it("B is denied at business 28 at audit 35, and the chain to C breaks with it", () => {
    const kernel = build();
    assert.equal(kernel.hasRole("B", "R", 28, 35), false);
    assert.equal(kernel.hasRole("C", "R", 28, 35), false);
  });
});

describe("multi-level expiry", () => {
  it("downstream delegation outliving the upstream one lapses with it", () => {
    const kernel = kernelWithRole();
    kernel.grantRole("A", "R", 0, INF, 0);
    kernel.delegate("A", "B", "R", 10, 50, 10);
    kernel.delegate("B", "C", "R", 20, 60, 20);
    assert.equal(kernel.hasRole("C", "R", 49, 100), true);
    assert.equal(kernel.hasRole("C", "R", 50, 100), false); // [10, 50) is half-open
    assert.equal(kernel.hasRole("C", "R", 55, 100), false);
  });

  it("downstream delegation is inactive before its own start", () => {
    const kernel = kernelWithRole();
    kernel.grantRole("A", "R", 0, INF, 0);
    kernel.delegate("A", "B", "R", 10, 50, 10);
    kernel.delegate("B", "C", "R", 20, 60, 20);
    assert.equal(kernel.hasRole("C", "R", 19, 100), false);
    assert.equal(kernel.hasRole("B", "R", 19, 100), true);
  });
});

describe("direct grants and permission union", () => {
  it("collects permissions across all held roles", () => {
    const kernel = kernelWithRole();
    kernel.registerRole({ id: "AUDITOR", permissions: new Set(["doc:read", "audit:log"]) });
    kernel.grantRole("U", "R", 0, 100, 0);
    kernel.grantRole("U", "AUDITOR", 50, 150, 0);
    assert.deepEqual(kernel.effectivePermissions("U", 75, 200), [
      "audit:log",
      "doc:read",
      "doc:write",
    ]);
    assert.deepEqual(kernel.effectivePermissions("U", 25, 200), ["doc:read", "doc:write"]);
  });

  it("grant revocation takes effect from its business effective time", () => {
    const kernel = kernelWithRole();
    kernel.grantRole("U", "R", 0, 100, 0);
    kernel.revokeGrant("U", "R", 40, 60);
    assert.equal(kernel.hasRole("U", "R", 39, 70), true);
    assert.equal(kernel.hasRole("U", "R", 40, 70), false);
    assert.equal(kernel.hasRole("U", "R", 40, 59), true); // not yet known at audit 59
  });
});

describe("delegation semantics", () => {
  it("a delegator who does not hold the role confers nothing", () => {
    const kernel = kernelWithRole();
    kernel.delegate("X", "Y", "R", 0, 100, 0);
    assert.equal(kernel.hasRole("Y", "R", 50, 50), false);
  });

  it("delegation cycles terminate and confer nothing without a grant", () => {
    const kernel = kernelWithRole();
    kernel.delegate("A", "B", "R", 0, 100, 0);
    kernel.delegate("B", "A", "R", 0, 100, 0);
    assert.equal(kernel.hasRole("A", "R", 50, 50), false);
    assert.equal(kernel.hasRole("B", "R", 50, 50), false);
  });

  it("three-level chains resolve transitively", () => {
    const kernel = kernelWithRole();
    kernel.grantRole("A", "R", 0, INF, 0);
    kernel.delegate("A", "B", "R", 0, 100, 0);
    kernel.delegate("B", "C", "R", 0, 100, 0);
    kernel.delegate("C", "D", "R", 0, 100, 0);
    assert.equal(kernel.hasRole("D", "R", 50, 50), true);
  });

  it("revoking a non-existent delegation throws", () => {
    const kernel = kernelWithRole();
    assert.throws(() => kernel.revokeDelegation("A", "B", "R", 10, 10), /no active delegation/);
  });
});
