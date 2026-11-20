import assert from "node:assert/strict";
import test from "node:test";
import {
  createDisclosureHost,
  evaluateDisclosure,
  polygonRegion,
  UnauthenticatedAssociationError,
  verifyCertificate,
} from "../index.ts";
import type { SignedRegionDeclaration } from "../index.ts";

function square(x: number, y: number, width: number, height: number): number[][] {
  return [
    [x, y],
    [x + width, y],
    [x + width, y + height],
    [x, y + height],
  ];
}

function claim(
  claimId: string,
  observerId: string,
  region: unknown,
  associationId?: string,
): SignedRegionDeclaration {
  return {
    claimId,
    observerId,
    associationId,
    signature: `sig-${claimId}`,
    region: region as never,
  };
}

test("two independently compliant claims can fail jointly", () => {
  const first = claim("a", "observer-a", polygonRegion(square(0, 0, 4, 4)), "trajectory-1");
  const second = claim(
    "b",
    "observer-b",
    polygonRegion(square(3, 0, 4, 4)),
    "trajectory-1",
  );
  const decision = evaluateDisclosure([first, second], { minComponentArea: 5 });
  assert.equal(decision.publishable, false);
  assert.equal(decision.groups.length, 1);
  assert.deepEqual(decision.groups[0]!.minimumComponentArea, {
    numerator: "4",
    denominator: "1",
  });

  const strict = evaluateDisclosure([first, second], { minComponentArea: "16" });
  assert.equal(strict.publishable, false);
  assert.deepEqual(strict.groups[0]!.minimumComponentArea, {
    numerator: "4",
    denominator: "1",
  });
});

test("exact threshold equality is allowed", () => {
  const narrow = polygonRegion([
    ["3999/2000", 0],
    ["4001/2000", 0],
    ["4001/2000", 4],
    ["3999/2000", 4],
  ]);
  const wide = polygonRegion(square(0, 0, 4, 4));
  const declarations = [
    claim("a", "observer-a", wide, "trajectory-1"),
    claim("b", "observer-b", narrow, "trajectory-1"),
  ];
  assert.equal(
    evaluateDisclosure(declarations, { minComponentArea: "1/250" }).publishable,
    true,
  );
  assert.equal(
    evaluateDisclosure(declarations, { minComponentArea: "1/249" }).publishable,
    false,
  );
});

test("unauthenticated hypothesis groups are never intersected", () => {
  const first = claim("a", "observer-a", polygonRegion(square(0, 0, 1, 1)));
  const second = claim(
    "b",
    "observer-b",
    polygonRegion([
      ["99/100", 0],
      ["199/100", 0],
      ["199/100", 1],
      ["99/100", 1],
    ]),
  );
  const separate = evaluateDisclosure([first, second], { minComponentArea: 1 });
  assert.equal(separate.publishable, true);
  assert.equal(separate.groups.length, 2);

  const linked = evaluateDisclosure(
    [
      { ...first, associationId: "same-trajectory" },
      { ...second, associationId: "same-trajectory" },
    ],
    { minComponentArea: 1 },
  );
  assert.equal(linked.publishable, false);
  assert.deepEqual(linked.groups[0]!.minimumComponentArea, {
    numerator: "1",
    denominator: "100",
  });

  assert.throws(
    () =>
      evaluateDisclosure(
        [
          { ...first, associationId: "one" },
          { ...second, associationId: "two" },
        ],
        { minComponentArea: 1 },
        {
          groups: [{ id: "bad", claimIds: ["a", "b"], authenticated: true }],
        },
      ),
    UnauthenticatedAssociationError,
  );
});

test("multiple signed observations from one observer share a feasible region", () => {
  const first = claim("a", "same-observer", polygonRegion(square(0, 0, 4, 4)));
  const second = claim("b", "same-observer", polygonRegion(square(3, 0, 4, 4)));
  const decision = evaluateDisclosure([first, second], { minComponentArea: 5 });
  assert.equal(decision.groups.length, 1);
  assert.equal(decision.groups[0]!.groupId, "observer:same-observer");
  assert.equal(decision.publishable, false);
  assert.deepEqual(decision.groups[0]!.minimumComponentArea, {
    numerator: "4",
    denominator: "1",
  });
});

test("anonymous batch membership does not authenticate an intersection", () => {
  const first = {
    ...claim("a", "observer-a", polygonRegion(square(0, 0, 1, 1))),
    batchId: "anonymous-batch-1",
  };
  const second = {
    ...claim(
      "b",
      "observer-b",
      polygonRegion([
        ["99/100", 0],
        ["199/100", 0],
        ["199/100", 1],
        ["99/100", 1],
      ]),
    ),
    batchId: "anonymous-batch-1",
  };
  const decision = evaluateDisclosure([first, second], { minComponentArea: 1 });
  assert.equal(decision.publishable, true);
  assert.equal(decision.groups.length, 2);
});

test("certificate is rational, independently verifiable, and coordinate-free", () => {
  const declarations = [
    claim("a", "observer-a", polygonRegion(square(0, 0, 8, 8)), "trajectory-1"),
    claim("b", "observer-b", polygonRegion(square(6, 0, 8, 8)), "trajectory-1"),
  ];
  const decision = evaluateDisclosure(declarations, { minComponentArea: "4" });
  assert.equal(decision.publishable, true);
  assert.deepEqual(decision.groups[0]!.minimumComponentArea, {
    numerator: "16",
    denominator: "1",
  });
  assert.deepEqual(verifyCertificate(decision.certificate, declarations, { minComponentArea: 4 }), {
    valid: true,
    errors: [],
  });

  const serialized = JSON.stringify(decision.certificate);
  for (const forbidden of ["outer", "holes", "vertices", "polygons", "\"x\"", "\"y\""]) {
    assert.equal(serialized.includes(forbidden), false, `certificate leaked ${forbidden}`);
  }

  const tampered = {
    ...decision.certificate,
    groups: decision.certificate.groups.map((group) => ({
      ...group,
      minimumComponentArea: { numerator: "999", denominator: "1" },
    })),
  };
  assert.equal(
    verifyCertificate(tampered, declarations, { minComponentArea: 4 }).valid,
    false,
  );
});

test("denied publication leaves history and last publication untouched", () => {
  const host = createDisclosureHost({ minComponentArea: 5 });
  host.addClaim(claim("a", "observer-a", polygonRegion(square(0, 0, 4, 4)), "trajectory-1"));
  host.addClaim({
    ...claim("b", "observer-b", polygonRegion(square(3, 0, 4, 4)), "trajectory-1"),
    signature: "sig-b",
  });
  const before = host.getHistory();
  const denied = host.publish();
  assert.equal(denied.publishable, false);
  assert.deepEqual(host.getHistory(), before);
  assert.equal(host.lastPublication, null);

  host.correctClaim("a", {
    region: polygonRegion(square(0, 0, 8, 8)),
    signature: "sig-a2",
  });
  const allowed = host.publish();
  assert.equal(allowed.publishable, true);
  const snapshot = host.lastPublication;
  assert.equal(snapshot?.certificate.certificateDigest, allowed.certificate.certificateDigest);

  host.correctClaim("a", {
    region: polygonRegion(square(0, 0, 4, 4)),
    signature: "sig-a3",
  });
  const historyBeforeDenied = host.getHistory();
  assert.equal(host.publish().publishable, false);
  assert.deepEqual(host.getHistory(), historyBeforeDenied);
  assert.equal(host.lastPublication, snapshot);
});

test("corrections and retractions are append-only", () => {
  const host = createDisclosureHost({ minComponentArea: 1 });
  host.addClaim(claim("a", "observer-a", polygonRegion(square(0, 0, 2, 2))));
  const original = host.getClaimMetadata("a")!;
  host.correctClaim("a", {
    region: polygonRegion(square(0, 0, 3, 3)),
    signature: "sig-a2",
  });
  const corrected = host.getClaimMetadata("a")!;
  assert.equal(corrected.revision, original.revision + 1);
  assert.notEqual(corrected.declarationDigest, original.declarationDigest);
  assert.deepEqual(
    host.getHistory().map((event) => event.type),
    ["claim-registered", "claim-corrected"],
  );

  host.retractClaim("a");
  const retracted = host.getClaimMetadata("a")!;
  assert.equal(retracted.active, false);
  assert.deepEqual(
    host.getHistory().map((event) => event.type),
    ["claim-registered", "claim-corrected", "claim-retracted"],
  );
  assert.equal(host.publish().publishable, false);
  assert.equal(host.getHistory().length, 3);
  assert.throws(() => host.correctClaim("a", polygonRegion(square(0, 0, 1, 1))));

  host.addClaim(claim("c", "observer-c", polygonRegion(square(0, 0, 2, 2))));
  host.updateClaim("c", { retracted: true });
  assert.equal(host.getClaimMetadata("c")!.active, false);
});

test("invalid signed declarations do not mutate history", () => {
  const host = createDisclosureHost({ minComponentArea: 1 }, { requireSignatures: true });
  assert.throws(() =>
    host.addClaim({
      claimId: "a",
      observerId: "observer-a",
      region: polygonRegion(square(0, 0, 2, 2)),
    }),
  );
  assert.deepEqual(host.getHistory(), []);

  host.addClaim({
    claimId: "b",
    observerId: "observer-b",
    signature: "sig-b",
    region: polygonRegion(square(0, 0, 2, 2)),
  });
  assert.throws(() =>
    host.correctRegion("b", polygonRegion(square(0, 0, 3, 3))),
  );
  assert.equal(host.getHistory().length, 1);
  host.correctClaim("b", {
    region: polygonRegion(square(0, 0, 3, 3)),
    signature: "sig-b2",
  });
  assert.equal(host.getHistory().length, 2);
});
