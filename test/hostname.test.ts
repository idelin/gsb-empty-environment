import { test } from "node:test";
import assert from "node:assert/strict";
import {
  HostnameError,
  certificateMatchesMx,
  hostnameMatchesPattern,
  mxMatchesPolicyEntry,
  normalizeHostname,
} from "../index.ts";

test("normalizeHostname lowercases and strips a single trailing dot", () => {
  assert.equal(normalizeHostname("MAIL.Example.COM."), "mail.example.com");
  assert.equal(normalizeHostname("mx1.example.com"), "mx1.example.com");
});

test("normalizeHostname rejects empty and malformed labels", () => {
  assert.throws(() => normalizeHostname(""), HostnameError);
  assert.throws(() => normalizeHostname("."), HostnameError);
  assert.throws(() => normalizeHostname("bad..example.com"), HostnameError);
  assert.throws(() => normalizeHostname("-bad.example.com"), HostnameError);
  assert.throws(() => normalizeHostname("bad-.example.com"), HostnameError);
});

test("wildcard covers exactly one label", () => {
  assert.equal(hostnameMatchesPattern("mail.example.com", "*.example.com"), true);
  // Multi-label subdomains must not match a single-label wildcard.
  assert.equal(hostnameMatchesPattern("a.b.example.com", "*.example.com"), false);
  // The bare suffix itself does not match.
  assert.equal(hostnameMatchesPattern("example.com", "*.example.com"), false);
});

test("wildcard is only valid as the full leftmost label", () => {
  assert.throws(() => hostnameMatchesPattern("mail.example.com", "*example.com"), HostnameError);
  assert.throws(() => hostnameMatchesPattern("mail.example.com", "mail.*.com"), HostnameError);
  assert.throws(() => hostnameMatchesPattern("x.example.com", "*"), HostnameError);
});

test("exact patterns are case- and trailing-dot insensitive", () => {
  assert.equal(hostnameMatchesPattern("MX1.Example.COM.", "mx1.example.com"), true);
  assert.equal(hostnameMatchesPattern("mx2.example.com", "mx1.example.com"), false);
});

test("deeper wildcard suffix still only covers one label", () => {
  assert.equal(hostnameMatchesPattern("smtp.eu.example.com", "*.eu.example.com"), true);
  assert.equal(hostnameMatchesPattern("a.smtp.eu.example.com", "*.eu.example.com"), false);
  // *.example.com must not reach into a deeper suffix.
  assert.equal(hostnameMatchesPattern("smtp.eu.example.com", "*.example.com"), false);
});

test("mxMatchesPolicyEntry mirrors wildcard semantics", () => {
  assert.equal(mxMatchesPolicyEntry("mail.example.com", "*.example.com"), true);
  assert.equal(mxMatchesPolicyEntry("a.b.example.com", "*.example.com"), false);
  assert.equal(mxMatchesPolicyEntry("evil-example.com", "*.example.com"), false);
});

test("certificateMatchesMx requires a matching presented identity", () => {
  assert.equal(
    certificateMatchesMx("mail.example.com", ["mx.other.net", "*.example.com"]),
    true,
  );
  assert.equal(
    certificateMatchesMx("a.b.example.com", ["*.example.com"]),
    false,
  );
  assert.equal(certificateMatchesMx("mail.example.com", ["mail.other.com"]), false);
});

test("certificate wildcard must itself be a full leftmost label", () => {
  assert.equal(
    certificateMatchesMx("mail.example.com", ["*example.com", "mail.other.net"]),
    false,
  );
});
