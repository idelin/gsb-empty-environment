import { test } from "node:test";
import assert from "node:assert/strict";
import {
  InMemoryPolicyCache,
  MtaStsClient,
  MtaStsPolicyError,
  parsePolicy,
  parseStsTxtRecords,
} from "../index.ts";
import { FakeClock, FakeFetcher, FakeResolver, policyText, policyUrl, txtRecord } from "./helpers.ts";

test("parsePolicy accepts a well-formed enforce policy", () => {
  const policy = parsePolicy(policyText("enforce", 86400, ["mail.example.com", "*.example.com"]));
  assert.deepEqual(policy, {
    mode: "enforce",
    maxAge: 86400,
    mx: ["mail.example.com", "*.example.com"],
  });
});

test("parsePolicy normalizes MX entries to lowercase and de-duplicates", () => {
  const body = [
    "version: STSv1",
    "mode: testing",
    "max_age: 60",
    "mx: MAIL.Example.COM",
    "mx: mail.example.com",
  ].join("\n");
  const policy = parsePolicy(body);
  assert.deepEqual(policy.mx, ["mail.example.com"]);
});

test("parsePolicy rejects malformed directives and missing required fields", () => {
  assert.throws(() => parsePolicy("max_age: 10\n"), MtaStsPolicyError);
  assert.throws(() => parsePolicy("mode: enforce\n"), MtaStsPolicyError);
  assert.throws(() => parsePolicy("garbage-line\n"), MtaStsPolicyError);
});

test("a policy without mx entries parses but permits no MX host", async () => {
  const clock = new FakeClock();
  const resolver = new FakeResolver();
  const fetcher = new FakeFetcher();
  resolver.setRecords("example.com", [txtRecord("id-1")]);
  fetcher.setResponse(
    policyUrl("example.com"),
    "version: STSv1\nmode: enforce\nmax_age: 86400\n",
  );
  const client = new MtaStsClient({
    resolver,
    fetcher,
    cache: new InMemoryPolicyCache(),
    now: () => clock.now(),
  });
  const result = await client.evaluate({
    domain: "example.com",
    mxHost: "mail.example.com",
    certificateNames: ["mail.example.com"],
  });
  assert.equal(result.action, "block");
  assert.equal(result.reason, "mx-mismatch");
});

test("parsePolicy rejects unsupported version, mode or non-numeric max_age", () => {
  assert.throws(
    () => parsePolicy("version: STSv2\nmode: enforce\nmax_age: 10\nmx: a.example.com\n"),
    MtaStsPolicyError,
  );
  assert.throws(
    () => parsePolicy("version: STSv1\nmode: audit\nmax_age: 10\nmx: a.example.com\n"),
    MtaStsPolicyError,
  );
  assert.throws(
    () => parsePolicy("version: STSv1\nmode: enforce\nmax_age: soon\nmx: a.example.com\n"),
    MtaStsPolicyError,
  );
});

test("parsePolicy rejects invalid wildcard MX entries", () => {
  assert.throws(
    () => parsePolicy(policyText("enforce", 10, ["*example.com"])),
    MtaStsPolicyError,
  );
  assert.throws(
    () => parsePolicy(policyText("enforce", 10, ["a.*.com"])),
    MtaStsPolicyError,
  );
  assert.throws(
    () => parsePolicy(policyText("enforce", 10, ["*"])),
    MtaStsPolicyError,
  );
});

test("parsePolicy ignores unknown future directives", () => {
  const body = [
    "version: STSv1",
    "mode: none",
    "max_age: 0",
    "mx: mx.example.com",
    "future_directive: value",
  ].join("\n");
  const policy = parsePolicy(body);
  assert.equal(policy.mode, "none");
});

test("parseStsTxtRecords returns the first valid STSv1 record", () => {
  const parsed = parseStsTxtRecords([
    "other=value",
    "v=STSv1; id=20261112T010101",
  ]);
  assert.deepEqual(parsed, { id: "20261112T010101" });
});

test("parseStsTxtRecords returns null when records are absent or malformed", () => {
  assert.equal(parseStsTxtRecords([]), null);
  assert.equal(parseStsTxtRecords(["v=STSv1; id="]), null);
  assert.equal(parseStsTxtRecords(["v=TLSRPTv1; rua=mailto:a@b.com"]), null);
});
