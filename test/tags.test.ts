import assert from "node:assert/strict";
import { test } from "node:test";
import {
  escapeTagValue,
  isClientOnlyTag,
  parseTags,
  serializeTags,
  tagsForRecipient,
  unescapeTagValue,
} from "../src/tags.ts";
import { parseMessage, serializeMessage } from "../src/message.ts";
import { ProtocolError } from "../src/errors.ts";

test("unescapes the message-tags escape sequences", () => {
  assert.equal(unescapeTagValue("a\\:b"), "a;b");
  assert.equal(unescapeTagValue("a\\sb"), "a b");
  assert.equal(unescapeTagValue("a\\\\b"), "a\\b");
  assert.equal(unescapeTagValue("a\\rb\\nc"), "a\rb\nc");
  // Unknown escapes lose the backslash; trailing backslash is dropped.
  assert.equal(unescapeTagValue("a\\xb\\"), "axb");
});

test("escapes semicolon, space, backslash, CR and LF for writing", () => {
  assert.equal(escapeTagValue("a;b"), "a\\:b");
  assert.equal(escapeTagValue("a b"), "a\\sb");
  assert.equal(escapeTagValue("a\\b"), "a\\\\b");
  assert.equal(escapeTagValue("a\rb\nc"), "a\\rb\\nc");
});

test("round-trips a value containing a semicolon and a backslash", () => {
  const malicious = "x;y\\z";
  const wire = escapeTagValue(malicious);
  assert.equal(wire, "x\\:y\\\\z");
  assert.equal(unescapeTagValue(wire), malicious);
});

test("escaped tag value cannot inject another tag on the wire", () => {
  const tags = parseTags("+foo=evil\\:injected=1");
  assert.equal(tags.get("+foo"), "evil;injected=1");
  assert.equal(serializeTags(tags), "+foo=evil\\:injected=1");
  // The serialized output contains only one tag boundary.
  assert.equal(serializeTags(tags).split(";").length, 1);
});

test("parseTags rejects empty names, illegal names and duplicates", () => {
  assert.throws(() => parseTags(""), ProtocolError);
  assert.throws(() => parseTags("a;;b"), ProtocolError);
  assert.throws(() => parseTags("bad name"), ProtocolError);
  assert.throws(() => parseTags("a=1;a=2"), ProtocolError);
});

test("parseTags supports vendor-prefixed and value-less tags", () => {
  const tags = parseTags("draft/foo=bar;+baz;vendor.com/+qux=v");
  assert.equal(tags.get("draft/foo"), "bar");
  assert.equal(tags.get("+baz"), true);
  assert.equal(tags.get("vendor.com/+qux"), "v");
});

test("isClientOnlyTag honors the vendor boundary", () => {
  assert.equal(isClientOnlyTag("+foo"), true);
  assert.equal(isClientOnlyTag("vendor.com/+foo"), true);
  assert.equal(isClientOnlyTag("foo"), false);
  assert.equal(isClientOnlyTag("vendor.com/foo"), false);
});

test("tagsForRecipient strips client-only tags without message-tags", () => {
  const tags = parseTags("+client=keep?;server=yes;vendor.com/+c=1");

  const withoutNegotiation = tagsForRecipient(tags, false);
  assert.deepEqual([...withoutNegotiation.keys()], ["server"]);

  const withNegotiation = tagsForRecipient(tags, true);
  assert.deepEqual([...withNegotiation.keys()], ["+client", "server", "vendor.com/+c"]);
});

test("a newline smuggled into a value is neutralized by serialization", () => {
  const message = parseMessage("@+x=a\\nb PRIVMSG bob :hi");
  assert.equal(message.tags.get("+x"), "a\nb");
  const wire = serializeMessage(message);
  assert.ok(!wire.includes("\n"));
  assert.ok(wire.includes("@+x=a\\nb "));
});
