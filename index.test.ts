import assert from "node:assert/strict";
import test from "node:test";

import {
  filterAggregateRecords,
  isValidAggregateRecord,
  validateAggregateRecord,
} from "./index.ts";
import { AggregateReporter, reportAggregateRecords } from "./reporter.ts";

const sameSiteRecord = {
  sourceId: 101,
  triggerId: 202,
  sourceSite: "https://shop.example",
  triggerSite: "https://shop.example",
  destination: "https://shop.example",
};

test("retains same-site aggregate records with numeric identifiers", () => {
  const result = filterAggregateRecords([
    sameSiteRecord,
    {
      source: { id: 7, site: "https://news.example", destination: "https://news.example" },
      trigger: { id: 8, site: "https://news.example", destination: "https://news.example" },
    },
  ]);

  assert.equal(result.accepted.length, 2);
  assert.deepEqual(result.rejected, []);
});

test("supports snake_case aggregate fields", () => {
  const record = {
    source_id: 301,
    trigger_id: 302,
    source_site: "https://publisher.example",
    trigger_site: "https://publisher.example",
    attribution_destination: "https://publisher.example",
  };

  assert.equal(isValidAggregateRecord(record), true);
});

test("rejects a record carrying a user identifier exactly once", () => {
  const records = [
    sameSiteRecord,
    { ...sameSiteRecord, triggerId: 203, userId: "person-42" },
  ];
  const result = filterAggregateRecords(records);

  assert.equal(result.accepted.length, 1);
  assert.equal(result.rejected.length, 1);
  assert.equal(result.rejected[0]?.reason, "user-identifier");
  assert.equal(result.rejected[0]?.record, records[1]);
});

test("rejects nested user identifiers and raw IP addresses", () => {
  const nestedUser = {
    source: { id: 1, site: "https://a.example" },
    trigger: { id: 2, site: "https://a.example", metadata: { email: "a@example" } },
  };
  const rawIp = {
    sourceId: 3,
    triggerId: 4,
    sourceSite: "https://a.example",
    triggerSite: "https://a.example",
    clientIp: "203.0.113.9",
  };

  const result = filterAggregateRecords([nestedUser, rawIp]);
  assert.deepEqual(
    result.rejected.map((item) => item.reason),
    ["user-identifier", "raw-ip"],
  );
});

test("rejects a cross-site pair with an incorrect destination exactly once", () => {
  const mismatch = {
    sourceId: 11,
    triggerId: 12,
    sourceSite: "https://publisher.example",
    triggerSite: "https://advertiser.example",
    sourceDestination: "https://advertiser.example",
    triggerDestination: "https://wrong.example",
  };

  const result = filterAggregateRecords([sameSiteRecord, mismatch]);
  assert.equal(result.accepted.length, 1);
  assert.equal(result.rejected.length, 1);
  assert.equal(result.rejected[0]?.reason, "destination-mismatch");
});

test("rejects conflicting destination and target fields", () => {
  const wrongTarget = {
    sourceId: 31,
    triggerId: 32,
    sourceSite: "https://publisher.example",
    triggerSite: "https://advertiser.example",
    destination: "https://advertiser.example",
    target: "https://wrong.example",
  };

  assert.equal(validateAggregateRecord(wrongTarget).ok, false);
  assert.equal(validateAggregateRecord(wrongTarget).reason, "destination-mismatch");
});

test("accepts a cross-site pair only when its declared destinations agree", () => {
  const consistent = {
    sourceId: 21,
    triggerId: 22,
    sourceSite: "https://publisher.example",
    triggerSite: "https://advertiser.example",
    sourceDestination: "https://advertiser.example",
    triggerDestination: "https://advertiser.example",
  };

  assert.equal(isValidAggregateRecord(consistent), true);
  assert.equal(
    isValidAggregateRecord({ ...consistent, triggerDestination: "https://other.example" }),
    false,
  );
});

test("rejects a cross-site pair whose destination matches neither site", () => {
  const wrongSite = {
    sourceId: 41,
    triggerId: 42,
    sourceSite: "https://publisher.example",
    triggerSite: "https://advertiser.example",
    destination: "https://unrelated.example",
  };

  const validation = validateAggregateRecord(wrongSite);
  assert.equal(validation.ok, false);
  assert.equal(validation.reason, "destination-mismatch");
});

test("rejects string, fractional, negative, and missing identifiers", () => {
  const invalidRecords = [
    { ...sameSiteRecord, sourceId: "101" },
    { ...sameSiteRecord, triggerId: 2.5 },
    { ...sameSiteRecord, sourceId: -1 },
    { triggerId: 202 },
  ];

  for (const record of invalidRecords) {
    assert.equal(validateAggregateRecord(record).ok, false);
  }
});

test("reporting component calls the importable library", () => {
  const reporter = new AggregateReporter();
  const result = reporter.report([sameSiteRecord, { ...sameSiteRecord, uid: "abc" }]);

  assert.equal(result.accepted.length, 1);
  assert.equal(result.rejected[0]?.reason, "user-identifier");
  assert.deepEqual(reportAggregateRecords([sameSiteRecord]), {
    accepted: [sameSiteRecord],
    rejected: [],
  });
});
