import { test } from "node:test";
import assert from "node:assert/strict";

import {
  runDomainLockTrial,
  adjudicateScenario,
  DocumentModel,
  SecurityError,
  createOrigin,
  parseOrigin,
  isSameOrigin,
  requestsOriginKeyedAgentCluster,
} from "../index.ts";

test("pipeline: two scenarios, each assigns the parent domain once per page", () => {
  const report = runDomainLockTrial();
  assert.equal(report.parentDomain, "example.com");
  assert.equal(report.scenarios.length, 2);

  const [withoutHeader, withHeader] = report.scenarios;

  assert.equal(withoutHeader.headerPresent, false);
  assert.equal(withoutHeader.originKeyed, false);
  assert.equal(withoutHeader.assignmentA.threw, false);
  assert.equal(withoutHeader.assignmentB.threw, false);
  assert.equal(withoutHeader.assignmentA.domainAfter, "example.com");
  assert.equal(withoutHeader.assignmentB.domainAfter, "example.com");

  assert.equal(withHeader.headerPresent, true);
  assert.equal(withHeader.originKeyed, true);
  assert.equal(withHeader.assignmentA.threw, true);
  assert.equal(withHeader.assignmentB.threw, true);
  assert.equal(withHeader.assignmentA.errorName, "SecurityError");
  assert.equal(withHeader.assignmentB.errorName, "SecurityError");
});

test("without header: domain relaxation makes different-port pages same-origin", () => {
  const { scenarios } = runDomainLockTrial();
  const withoutHeader = scenarios[0];

  // The origin tuple still differs (host and port), so strict comparison
  // remains false even in the legacy path.
  assert.equal(withoutHeader.strictSameOrigin, false);
  // But the effective DOM-access rule treats the shortened domains as equal.
  assert.equal(withoutHeader.effectiveSameOrigin, true);
});

test("with header: assignment fails and host + port stay authoritative", () => {
  const { scenarios } = runDomainLockTrial();
  const withHeader = scenarios[1];

  assert.equal(withHeader.assignmentA.threw, true);
  assert.equal(withHeader.assignmentB.threw, true);
  // Effective domain is unchanged after the failed assignment.
  assert.equal(withHeader.assignmentA.domainAfter, "a.example.com");
  assert.equal(withHeader.assignmentB.domainAfter, "b.example.com");

  assert.equal(withHeader.strictSameOrigin, false);
  assert.equal(withHeader.effectiveSameOrigin, false);
});

test("with header: a single setter attempt is recorded and the origin tuple is untouched", () => {
  const origin = createOrigin("https", "a.example.com", 8080);
  const doc = new DocumentModel({ origin, originAgentClusterHeader: "?1" });

  assert.throws(
    () => doc.setDomain("example.com"),
    (error: unknown) =>
      error instanceof SecurityError && error.name === "SecurityError",
  );
  assert.equal(doc.domainAssignmentCount, 1);
  assert.equal(doc.getDomain(), "a.example.com");
  assert.equal(doc.origin.port, 8080);
});

test("without header: setter shortens domain but never mutates the origin port", () => {
  const origin = parseOrigin("https://a.example.com:8080/");
  const doc = new DocumentModel({ origin });

  doc.setDomain("example.com");
  assert.equal(doc.getDomain(), "example.com");
  assert.equal(doc.origin.host, "a.example.com");
  assert.equal(doc.origin.port, 8080);
});

test("different ports are distinct origins under strict tuple comparison", () => {
  const a = parseOrigin("https://a.example.com:8080/");
  const b = parseOrigin("https://a.example.com:9090/");
  assert.equal(isSameOrigin(a, b), false);

  const samePort = parseOrigin("https://a.example.com:8080/path");
  assert.equal(isSameOrigin(a, samePort), true);
});

test("canAccess: header forces strict host+port; absent header allows relaxed match", () => {
  const lockedA = new DocumentModel({
    origin: createOrigin("https", "a.example.com", 8080),
    originAgentClusterHeader: "?1",
  });
  const lockedB = new DocumentModel({
    origin: createOrigin("https", "b.example.com", 9090),
    originAgentClusterHeader: "?1",
  });
  assert.equal(lockedA.canAccess(lockedB), false);

  const relaxedA = new DocumentModel({
    origin: createOrigin("https", "a.example.com", 8080),
  });
  const relaxedB = new DocumentModel({
    origin: createOrigin("https", "b.example.com", 9090),
  });
  relaxedA.setDomain("example.com");
  relaxedB.setDomain("example.com");
  assert.equal(relaxedA.canAccess(relaxedB), true);
});

test("legacy setter rejects a domain that is not a parent of the host", () => {
  const doc = new DocumentModel({
    origin: createOrigin("https", "a.example.com", 8080),
  });
  assert.throws(
    () => doc.setDomain("other.org"),
    (error: unknown) => error instanceof SecurityError,
  );
  assert.equal(doc.getDomain(), "a.example.com");
});

test("Origin-Agent-Cluster header parsing only honors the boolean ?1", () => {
  assert.equal(requestsOriginKeyedAgentCluster("?1"), true);
  assert.equal(requestsOriginKeyedAgentCluster("  ?1  "), true);
  assert.equal(requestsOriginKeyedAgentCluster("?0"), false);
  assert.equal(requestsOriginKeyedAgentCluster(null), false);
  assert.equal(requestsOriginKeyedAgentCluster(undefined), false);
  assert.equal(requestsOriginKeyedAgentCluster(""), false);
  assert.equal(requestsOriginKeyedAgentCluster("?1; foo"), false);
});

test("adjudicateScenario: explicit custom inputs are supported", () => {
  const verdict = adjudicateScenario({
    label: "custom",
    originAgentClusterHeader: "?1",
    originA: createOrigin("http", "shop.app.test", 3000),
    originB: createOrigin("http", "cdn.app.test", 4000),
    parentDomain: "app.test",
  });
  assert.equal(verdict.originKeyed, true);
  assert.equal(verdict.assignmentA.threw, true);
  assert.equal(verdict.effectiveSameOrigin, false);
  assert.equal(verdict.strictSameOrigin, false);
});
