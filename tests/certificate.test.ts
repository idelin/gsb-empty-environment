import test from "node:test";
import assert from "node:assert/strict";
import { AnchorService, generateAnchorKeys } from "../src/anchor.ts";
import { CertificateIssuer, verifyCertificate } from "../src/certificate.ts";

const facts = [
  "order#A1001 amount=500 currency=CNY",
  "order#A1002 amount=320 currency=CNY",
  "order#A1003 amount=780 currency=CNY",
];

function setup() {
  const keys = generateAnchorKeys();
  const issuer = new CertificateIssuer();
  issuer.ingest(facts);
  const service = new AnchorService(keys, () => 1000);
  const anchor = service.anchor(issuer.root);
  return { keys, issuer, anchor };
}

test("acceptance: fact anchored at t=1000 is proven to exist before t=1000", () => {
  const { keys, issuer, anchor } = setup();
  assert.equal(anchor.timestamp, 1000);
  const cert = issuer.issue(facts[1], anchor, 1500);
  const res = verifyCertificate(cert, facts[1], keys.publicKey, {
    notLaterThan: 1000,
  });
  assert.equal(res.ok, true);
  assert.deepEqual(res.checks, {
    leafHashMatches: true,
    merkleProofValid: true,
    anchorSignatureValid: true,
    anchoredBeforeDeadline: true,
  });
});

test("tampered fact cannot be proven", () => {
  const { keys, issuer, anchor } = setup();
  const cert = issuer.issue(facts[1], anchor, 1500);
  const res = verifyCertificate(
    cert,
    "order#A1002 amount=999 currency=CNY",
    keys.publicKey,
    { notLaterThan: 1000 },
  );
  assert.equal(res.ok, false);
  assert.equal(res.checks.leafHashMatches, false);
});

test("certificate bound to a later anchor fails the t=1000 bound", () => {
  const { keys, issuer } = setup();
  const lateService = new AnchorService(keys, () => 2000);
  const lateAnchor = lateService.anchor(issuer.root);
  const cert = issuer.issue(facts[0], lateAnchor, 2500);
  const res = verifyCertificate(cert, facts[0], keys.publicKey, {
    notLaterThan: 1000,
  });
  assert.equal(res.ok, false);
  assert.equal(res.checks.anchoredBeforeDeadline, false);
});

test("certificate with swapped proof fails", () => {
  const { keys, issuer, anchor } = setup();
  const certA = issuer.issue(facts[0], anchor, 1500);
  const certB = issuer.issue(facts[1], anchor, 1500);
  const swapped = { ...certA, proof: certB.proof };
  const res = verifyCertificate(swapped, facts[0], keys.publicKey, {
    notLaterThan: 1000,
  });
  assert.equal(res.ok, false);
  assert.equal(res.checks.merkleProofValid, false);
});

test("issuing a certificate for unknown data throws", () => {
  const { issuer, anchor } = setup();
  assert.throws(() => issuer.issue("not-ingested", anchor, 1500));
});
