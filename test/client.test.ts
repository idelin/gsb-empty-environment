import { test } from "node:test";
import assert from "node:assert/strict";
import {
  InMemoryPolicyCache,
  MtaStsClient,
  type EvaluationInput,
} from "../index.ts";
import {
  FakeClock,
  FakeFetcher,
  FakeResolver,
  RecordingLogger,
  policyText,
  policyUrl,
  txtRecord,
} from "./helpers.ts";

const DOMAIN = "example.com";
const ENFORCE_MX = ["mail.example.com", "*.example.com"];

interface Harness {
  clock: FakeClock;
  resolver: FakeResolver;
  fetcher: FakeFetcher;
  logger: RecordingLogger;
  cache: InMemoryPolicyCache;
  client: MtaStsClient;
}

function makeHarness(): Harness {
  const clock = new FakeClock();
  const resolver = new FakeResolver();
  const fetcher = new FakeFetcher();
  const logger = new RecordingLogger();
  const cache = new InMemoryPolicyCache();
  const client = new MtaStsClient({
    resolver,
    fetcher,
    logger,
    cache,
    now: () => clock.now(),
  });
  return { clock, resolver, fetcher, logger, cache, client };
}

function delivery(mxHost = "mail.example.com", certificateNames = [mxHost]): EvaluationInput {
  return { domain: DOMAIN, mxHost, certificateNames };
}

test("fetches over HTTPS on first lookup and caches the policy", async () => {
  const { client, resolver, fetcher, cache, clock } = makeHarness();
  resolver.setRecords(DOMAIN, [txtRecord("id-1")]);
  fetcher.setResponse(policyUrl(DOMAIN), policyText("enforce", 86400, ENFORCE_MX));

  const result = await client.evaluate(delivery());
  assert.equal(result.action, "deliver");
  assert.equal(result.reason, "mx-match");
  assert.equal(fetcher.calls.length, 1);

  const cached = cache.get(DOMAIN, clock.now());
  assert.ok(cached);
  assert.equal(cached?.id, "id-1");
  assert.equal(cached?.expiresAt, clock.now() + 86400 * 1000);
});

test("same id does not trigger another HTTPS fetch", async () => {
  const { client, resolver, fetcher, clock } = makeHarness();
  resolver.setRecords(DOMAIN, [txtRecord("id-1")]);
  fetcher.setResponse(policyUrl(DOMAIN), policyText("enforce", 86400, ENFORCE_MX));

  await client.evaluate(delivery());
  clock.advance(10_000);
  const second = await client.evaluate(delivery());

  assert.equal(second.action, "deliver");
  assert.equal(fetcher.calls.length, 1);
});

test("changed id refetches and refreshes the cached policy", async () => {
  const { client, resolver, fetcher, clock } = makeHarness();
  resolver.setRecords(DOMAIN, [txtRecord("id-1")]);
  fetcher.setResponse(
    policyUrl(DOMAIN),
    policyText("enforce", 86400, ENFORCE_MX),
  );
  await client.evaluate(delivery());

  fetcher.setResponse(
    policyUrl(DOMAIN),
    policyText("enforce", 86400, ["newhost.example.com"]),
  );
  resolver.setRecords(DOMAIN, [txtRecord("id-2")]);
  const result = await client.evaluate(delivery("newhost.example.com"));

  assert.equal(fetcher.calls.length, 2);
  assert.equal(result.action, "deliver");
  const cached = client.getCache().get(DOMAIN, clock.now());
  assert.deepEqual(cached?.policy.mx, ["newhost.example.com"]);
});

test("non-HTTPS policy URL is never fetched and delivery fails closed", async () => {
  const { client, resolver, fetcher } = makeHarness();
  resolver.setRecords(DOMAIN, [txtRecord("id-1")]);
  fetcher.fail(
    policyUrl(DOMAIN),
    new Error("refusing to fetch MTA-STS policy over non-HTTPS URL: http://mta-sts.example.com/..."),
  );

  const result = await client.evaluate(delivery());
  assert.equal(result.action, "block");
  assert.equal(result.reason, "policy-fetch-failure");
});

test("malformed policy never overwrites a previously cached good policy", async () => {
  const { client, resolver, fetcher, clock, logger } = makeHarness();
  resolver.setRecords(DOMAIN, [txtRecord("id-1")]);
  fetcher.setResponse(policyUrl(DOMAIN), policyText("enforce", 86400, ENFORCE_MX));
  await client.evaluate(delivery());

  resolver.setRecords(DOMAIN, [txtRecord("id-forged")]);
  fetcher.setResponse(policyUrl(DOMAIN), "mode: enforce\n");
  const result = await client.evaluate(delivery());

  assert.equal(result.action, "deliver");
  assert.equal(result.policy?.maxAge, 86400);
  assert.equal(logger.warnings.length, 0);
  assert.equal(client.getCache().get(DOMAIN, clock.now())?.id, "id-1");
});

test("stripped DNS record keeps enforcing the cached policy until max_age", async () => {
  const { client, resolver, fetcher, clock } = makeHarness();
  resolver.setRecords(DOMAIN, [txtRecord("id-1")]);
  fetcher.setResponse(policyUrl(DOMAIN), policyText("enforce", 60, ENFORCE_MX));
  await client.evaluate(delivery());

  // Attacker removes the _mta-sts TXT record.
  resolver.removeRecords(DOMAIN);

  clock.advance(30_000);
  const withinLifetime = await client.evaluate(delivery("attacker.example.net"));
  assert.equal(withinLifetime.action, "block");
  assert.equal(withinLifetime.reason, "mx-mismatch");
  assert.equal(fetcher.calls.length, 1);

  // After max_age the entry expires; with no record there is no policy.
  clock.advance(31_000);
  const expired = await client.evaluate(delivery("attacker.example.net"));
  assert.equal(expired.action, "deliver");
  assert.equal(expired.reason, "no-policy");
});

test("enforce mode blocks an MX host not listed by the policy (MX spoofing)", async () => {
  const { client, resolver, fetcher } = makeHarness();
  resolver.setRecords(DOMAIN, [txtRecord("id-1")]);
  fetcher.setResponse(policyUrl(DOMAIN), policyText("enforce", 86400, ENFORCE_MX));

  const spoofed = await client.evaluate(
    delivery("mail.example.com.attacker.net", ["mail.example.com.attacker.net"]),
  );
  assert.equal(spoofed.action, "block");
  assert.equal(spoofed.reason, "mx-mismatch");

  const lookalike = await client.evaluate(
    delivery("notexample.com", ["notexample.com"]),
  );
  assert.equal(lookalike.action, "block");
});

test("enforce mode blocks multi-label host against single-label wildcard", async () => {
  const { client, resolver, fetcher } = makeHarness();
  resolver.setRecords(DOMAIN, [txtRecord("id-1")]);
  fetcher.setResponse(policyUrl(DOMAIN), policyText("enforce", 86400, ["*.example.com"]));

  const singleLabel = await client.evaluate(
    delivery("one.example.com", ["one.example.com"]),
  );
  assert.equal(singleLabel.action, "deliver");

  const multiLabel = await client.evaluate(
    delivery("a.b.example.com", ["a.b.example.com"]),
  );
  assert.equal(multiLabel.action, "block");
  assert.equal(multiLabel.reason, "mx-mismatch");
});

test("enforce mode blocks when the certificate identity does not match the MX", async () => {
  const { client, resolver, fetcher } = makeHarness();
  resolver.setRecords(DOMAIN, [txtRecord("id-1")]);
  fetcher.setResponse(policyUrl(DOMAIN), policyText("enforce", 86400, ENFORCE_MX));

  const result = await client.evaluate(
    delivery("mail.example.com", ["cert.other.net"]),
  );
  assert.equal(result.action, "block");
  assert.equal(result.reason, "certificate-mismatch");

  // A certificate wildcard still cannot span multiple labels.
  const certWildcard = await client.evaluate(
    delivery("a.b.example.com", ["*.example.com"]),
  );
  assert.equal(certWildcard.action, "block");
});

test("testing mode records failures but never blocks", async () => {
  const { client, resolver, fetcher, logger } = makeHarness();
  resolver.setRecords(DOMAIN, [txtRecord("id-1")]);
  fetcher.setResponse(policyUrl(DOMAIN), policyText("testing", 86400, ENFORCE_MX));

  const badMx = await client.evaluate(
    delivery("attacker.example.net", ["attacker.example.net"]),
  );
  assert.equal(badMx.action, "deliver");
  assert.equal(badMx.testingOverride, true);
  assert.equal(badMx.reason, "mx-mismatch");
  assert.equal(logger.warnings.length, 1);

  const badCert = await client.evaluate(
    delivery("mail.example.com", ["cert.other.net"]),
  );
  assert.equal(badCert.action, "deliver");
  assert.equal(badCert.testingOverride, true);
  assert.equal(badCert.reason, "certificate-mismatch");
  assert.equal(logger.warnings.length, 2);

  const ok = await client.evaluate(delivery());
  assert.equal(ok.action, "deliver");
  assert.equal(ok.testingOverride, false);
});

test("policy mode none allows delivery", async () => {
  const { client, resolver, fetcher } = makeHarness();
  resolver.setRecords(DOMAIN, [txtRecord("id-1")]);
  fetcher.setResponse(policyUrl(DOMAIN), policyText("none", 86400, ENFORCE_MX));

  const result = await client.evaluate(delivery("anything.example.net"));
  assert.equal(result.action, "deliver");
  assert.equal(result.reason, "policy-mode-none");
});

test("fetch failure with no cached policy blocks; cached policy stays authoritative", async () => {
  const { client, resolver, fetcher, clock } = makeHarness();
  resolver.setRecords(DOMAIN, [txtRecord("id-1")]);
  fetcher.setResponse(policyUrl(DOMAIN), policyText("enforce", 86400, ENFORCE_MX));
  await client.evaluate(delivery());

  resolver.setRecords(DOMAIN, [txtRecord("id-2")]);
  fetcher.fail(policyUrl(DOMAIN), new Error("tls handshake failure"));
  const result = await client.evaluate(delivery("attacker.example.net"));
  assert.equal(result.action, "block");
  assert.equal(result.reason, "mx-mismatch");
  assert.equal(client.getCache().get(DOMAIN, clock.now())?.id, "id-1");

  const fresh = new MtaStsClient({
    resolver,
    fetcher,
    now: () => clock.now(),
  });
  const noCache = await fresh.evaluate(delivery());
  assert.equal(noCache.action, "block");
  assert.equal(noCache.reason, "policy-fetch-failure");
});

test("DNS errors fail closed while a cached policy still governs delivery", async () => {
  const { client, resolver, fetcher } = makeHarness();
  resolver.setRecords(DOMAIN, [txtRecord("id-1")]);
  fetcher.setResponse(policyUrl(DOMAIN), policyText("enforce", 86400, ENFORCE_MX));
  await client.evaluate(delivery());

  resolver.failWith(DOMAIN, new Error("SERVFAIL"));
  const cachedDecision = await client.evaluate(delivery("attacker.example.net"));
  assert.equal(cachedDecision.action, "block");
  assert.equal(cachedDecision.reason, "mx-mismatch");

  const fresh = new MtaStsClient({ resolver, fetcher });
  const noCache = await fresh.evaluate(delivery());
  assert.equal(noCache.action, "block");
  assert.equal(noCache.reason, "dns-error");
});

test("cache snapshot round-trips expiry state across instances", async () => {
  const clock = new FakeClock();
  const cache = new InMemoryPolicyCache();
  cache.set(DOMAIN, "id-1", { mode: "enforce", maxAge: 60, mx: ENFORCE_MX }, clock.now());

  const restored = new InMemoryPolicyCache();
  restored.load(cache.snapshot());
  assert.ok(restored.get(DOMAIN, clock.now() + 30_000));
  assert.equal(restored.get(DOMAIN, clock.now() + 61_000), null);
});
