import assert from "node:assert/strict";
import { test } from "node:test";

import {
  discoverLrddTemplate,
  parseAcct,
  resolveAccount,
  resolveWebFinger,
  validateJrd,
  validateLrddTemplate,
} from "../index.ts";
import { createStubFetch, hostMeta, jrd } from "../test-support/helpers.ts";

const DOMAIN = "example.example";
const ACCT = `acct:alice@${DOMAIN}`;
const WEBFINGER_URL =
  `https://${DOMAIN}/.well-known/webfinger?resource=${encodeURIComponent(ACCT)}`;
const HOST_META_URL = `https://${DOMAIN}/.well-known/host-meta`;

function validJrd(subject = ACCT): string {
  return jrd({
    subject,
    links: [{ rel: "self", href: `https://${DOMAIN}/profile` }],
  });
}

test("acct parsing keeps the query host from the acct URI", () => {
  const parsed = parseAcct("acct:Alice@Example.Example.");
  assert.equal(parsed.host, DOMAIN);
  assert.equal(parsed.hostname, DOMAIN);
  assert.equal(parsed.user, "Alice");
});

test("a cross-host redirect is rejected without contacting the other host", async () => {
  const evilUrl =
    `https://evil.example/.well-known/webfinger?resource=${encodeURIComponent(ACCT)}`;
  const stub = createStubFetch({
    [WEBFINGER_URL]: {
      status: 302,
      headers: { location: evilUrl },
    },
    [evilUrl]: { body: validJrd("acct:alice@evil.example") },
  });

  const result = await resolveAccount(ACCT, { fetch: stub.fetch });

  assert.equal(result, null);
  assert.deepEqual(stub.calls, [WEBFINGER_URL]);
  assert.equal(stub.calls.includes(evilUrl), false);
});

test("two same-host redirects are followed, a third is refused", async () => {
  const stillHost = `https://${DOMAIN}/redirect-1`;
  const secondHost = `https://${DOMAIN}/redirect-2`;
  const thirdHost = `https://${DOMAIN}/redirect-3`;

  const accepted = createStubFetch({
    [WEBFINGER_URL]: {
      status: 301,
      headers: { location: `https://${DOMAIN}/redirect-1` },
    },
    [stillHost]: { status: 307, headers: { location: secondHost } },
    [secondHost]: { body: validJrd() },
  });
  const resolved = await resolveAccount(ACCT, { fetch: accepted.fetch });
  assert.notEqual(resolved, null);
  assert.equal(resolved?.redirects, 2);
  assert.deepEqual(accepted.calls, [WEBFINGER_URL, stillHost, secondHost]);

  const refused = createStubFetch({
    [WEBFINGER_URL]: { status: 302, headers: { location: stillHost } },
    [stillHost]: { status: 302, headers: { location: secondHost } },
    [secondHost]: { status: 302, headers: { location: thirdHost } },
    [thirdHost]: { body: validJrd() },
  });
  const result = await resolveAccount(ACCT, { fetch: refused.fetch });
  assert.equal(result, null);
  assert.deepEqual(refused.calls, [WEBFINGER_URL, stillHost, secondHost]);
});

test("an http redirect is refused because only https is requested", async () => {
  const insecure = `http://${DOMAIN}/insecure`;
  const stub = createStubFetch({
    [WEBFINGER_URL]: { status: 302, headers: { location: insecure } },
    [insecure]: { body: validJrd() },
  });

  const result = await resolveAccount(ACCT, { fetch: stub.fetch });
  assert.equal(result, null);
  assert.deepEqual(stub.calls, [WEBFINGER_URL]);
  assert.equal(stub.calls[0]?.startsWith("https://"), true);
});

test("a subject on another domain discards the whole JRD", async () => {
  const stub = createStubFetch({
    [WEBFINGER_URL]: {
      body: jrd({
        subject: "acct:alice@evil.example",
        links: [{ rel: "self", href: `https://${DOMAIN}/looks-safe` }],
      }),
    },
  });

  assert.equal(await resolveWebFinger(ACCT, { fetch: stub.fetch }), null);
  assert.equal(
    validateJrd(
      {
        subject: "acct:alice@evil.example",
        links: [{ rel: "self", href: `https://${DOMAIN}/looks-safe` }],
      },
      DOMAIN,
    ),
    null,
  );
});

test("subject host comparison is case-insensitive and still acct-scoped", async () => {
  const stub = createStubFetch({
    [WEBFINGER_URL]: { body: validJrd("ACCT:Bob@EXAMPLE.EXAMPLE.") },
  });

  const resolved = await resolveAccount(ACCT, { fetch: stub.fetch });
  assert.notEqual(resolved, null);
  assert.equal(resolved?.subject, "ACCT:Bob@EXAMPLE.EXAMPLE.");
});

test("subject matching compares the acct domain, not a non-default port", () => {
  const document = {
    subject: "acct:alice@example.example",
    links: [],
  };

  assert.notEqual(validateJrd(document, "example.example:8443"), null);
  assert.equal(
    validateJrd({ ...document, subject: "acct:alice@evil.example" }, "example.example:8443"),
    null,
  );
});

test("host-meta lrdd templates on another host are rejected", async () => {
  const externalTemplate =
    `https://evil.example/.well-known/webfinger?resource={uri}`;
  const stub = createStubFetch({
    [HOST_META_URL]: { body: hostMeta(externalTemplate) },
  });

  assert.equal(
    await discoverLrddTemplate(ACCT, { fetch: stub.fetch }),
    null,
  );
  assert.deepEqual(stub.calls, [HOST_META_URL]);
  assert.equal(stub.calls.includes("https://evil.example/.well-known/webfinger?resource={uri}"), false);

  assert.equal(
    validateLrddTemplate(externalTemplate, DOMAIN),
    null,
  );
});

test("host-meta lrdd must be https and contain {uri}", () => {
  const httpsWithoutVariable =
    `https://${DOMAIN}/.well-known/webfinger?resource=acct%3Aalice`;
  const httpWithVariable =
    `http://${DOMAIN}/.well-known/webfinger?resource={uri}`;
  const hostInVariable = "https://{uri}/.well-known/webfinger";

  assert.equal(validateLrddTemplate(httpsWithoutVariable, DOMAIN), null);
  assert.equal(validateLrddTemplate(httpWithVariable, DOMAIN), null);
  assert.equal(validateLrddTemplate(hostInVariable, DOMAIN), null);
});

test("a safe lrdd template is used for the WebFinger request", async () => {
  const template =
    `https://${DOMAIN}/.well-known/webfinger?resource={uri}`;
  const templateJrdUrl =
    `https://${DOMAIN}/.well-known/webfinger?resource=${encodeURIComponent(ACCT)}`;
  const stub = createStubFetch({
    [HOST_META_URL]: { body: hostMeta(template) },
    [templateJrdUrl]: { body: validJrd() },
  });

  const resolved = await resolveAccount(ACCT, {
    fetch: stub.fetch,
    useHostMeta: true,
  });

  assert.notEqual(resolved, null);
  assert.equal(resolved?.endpoint, templateJrdUrl);
  assert.deepEqual(stub.calls, [HOST_META_URL, templateJrdUrl]);
});

test("an unsafe discovered lrdd template fails closed instead of falling back", async () => {
  const stub = createStubFetch({
    [HOST_META_URL]: {
      body: hostMeta("https://evil.example/webfinger?resource={uri}"),
    },
    [WEBFINGER_URL]: { body: validJrd() },
  });

  const resolved = await resolveAccount(ACCT, {
    fetch: stub.fetch,
    useHostMeta: true,
  });

  assert.equal(resolved, null);
  assert.deepEqual(stub.calls, [HOST_META_URL]);
});

test("a caller-supplied lrdd template is validated before any request", async () => {
  const stub = createStubFetch({
    [WEBFINGER_URL]: { body: validJrd() },
  });

  const resolved = await resolveAccount(ACCT, {
    fetch: stub.fetch,
    lrddTemplate: "https://evil.example/webfinger?resource={uri}",
  });

  assert.equal(resolved, null);
  assert.deepEqual(stub.calls, []);
});

test("host-meta 404 falls back to the standard same-host endpoint", async () => {
  const stub = createStubFetch({
    [HOST_META_URL]: { status: 404, body: "not found" },
    [WEBFINGER_URL]: { body: validJrd() },
  });

  const resolved = await resolveAccount(ACCT, {
    fetch: stub.fetch,
    useHostMeta: true,
  });

  assert.notEqual(resolved, null);
  assert.deepEqual(stub.calls, [HOST_META_URL, WEBFINGER_URL]);
});

test("a cross-host host-meta redirect is not followed", async () => {
  const evilUrl = "https://evil.example/.well-known/host-meta";
  const stub = createStubFetch({
    [HOST_META_URL]: { status: 302, headers: { location: evilUrl } },
    [evilUrl]: { body: hostMeta(`https://evil.example/webfinger?resource={uri}`) },
    [WEBFINGER_URL]: { body: validJrd() },
  });

  const resolved = await resolveAccount(ACCT, {
    fetch: stub.fetch,
    useHostMeta: true,
  });

  assert.notEqual(resolved, null);
  assert.deepEqual(stub.calls, [HOST_META_URL, WEBFINGER_URL]);
  assert.equal(stub.calls.includes(evilUrl), false);
});

test("an invalid first lrdd link does not hide a later safe link", async () => {
  const safeTemplate =
    `https://${DOMAIN}/.well-known/webfinger?resource={uri}`;
  const templateJrdUrl =
    `https://${DOMAIN}/.well-known/webfinger?resource=${encodeURIComponent(ACCT)}`;
  const xml = [
    '<XRD xmlns="http://docs.oasis-open.org/ns/xri/xrd-1.0">',
    '<Link rel="lrdd" template="https://evil.example/webfinger?resource={uri}" />',
    `<Link rel="lrdd" template="${safeTemplate}" />`,
    "</XRD>",
  ].join("");
  const stub = createStubFetch({
    [HOST_META_URL]: { body: xml },
    [templateJrdUrl]: { body: validJrd() },
  });

  const resolved = await resolveAccount(ACCT, {
    fetch: stub.fetch,
    useHostMeta: true,
  });

  assert.notEqual(resolved, null);
  assert.deepEqual(stub.calls, [HOST_META_URL, templateJrdUrl]);
});

test("self links are filtered by host, with external hosts only by whitelist", async () => {
  const document = {
    subject: ACCT,
    links: [
      { rel: "self", href: `https://${DOMAIN}/self` },
      { rel: "self", href: `https://example.example:8443/self` },
      { rel: "self", href: "//evil.example/self" },
      { rel: "self", href: "https://example.example.evil/self" },
      { rel: "SELF profile", href: "https://evil.example/self" },
      { rel: "self", href: "https://trusted.example/self" },
      { rel: "profile-page", href: "https://evil.example/profile" },
      { rel: "self", href: "mailto:alice@example.example" },
    ],
  };

  const withoutWhitelist = validateJrd(document, DOMAIN);
  assert.deepEqual(
    withoutWhitelist?.links?.map((link) => link.href),
    [`https://${DOMAIN}/self`, "https://evil.example/profile"],
  );

  const withWhitelist = validateJrd(document, DOMAIN, [
    "TRUSTED.EXAMPLE",
    `example.example:8443`,
  ]);
  assert.deepEqual(
    withWhitelist?.links?.map((link) => link.href),
    [
      `https://${DOMAIN}/self`,
      "https://example.example:8443/self",
      "https://trusted.example/self",
      "https://evil.example/profile",
    ],
  );
});

test("a resolved JRD keeps non-self links but drops external self links", async () => {
  const stub = createStubFetch({
    [WEBFINGER_URL]: {
      body: jrd({
        subject: ACCT,
        links: [
          { rel: "self", href: "https://evil.example/self" },
          { rel: "profile-page", href: "https://evil.example/profile" },
        ],
      }),
    },
  });

  const resolved = await resolveAccount(ACCT, { fetch: stub.fetch });
  assert.notEqual(resolved, null);
  assert.deepEqual(resolved?.links.map((link) => link.href), [
    "https://evil.example/profile",
  ]);
});
