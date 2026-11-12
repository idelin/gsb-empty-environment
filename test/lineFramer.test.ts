import assert from "node:assert/strict";
import { test } from "node:test";
import { LineFramer, MAX_LINE_LENGTH } from "../src/lineFramer.ts";
import { ProtocolError } from "../src/errors.ts";

function collect(): { lines: string[]; framer: LineFramer } {
  const lines: string[] = [];
  const framer = new LineFramer((line) => lines.push(line));
  return { lines, framer };
}

test("delivers lines split on CRLF, including across chunks", () => {
  const { lines, framer } = collect();
  framer.feed("NICK alice\r\nUSER a 0 * :");
  framer.feed("Alice\r\n");
  assert.deepEqual(lines, ["NICK alice", "USER a 0 * :Alice"]);
});

test("rejects a bare LF (inline newline) and resets state", () => {
  const { lines, framer } = collect();
  assert.throws(() => framer.feed("PRIVMSG bob :hello\nworld\r\n"), ProtocolError);
  // After reset the stream can recover on the next framing-clean line.
  framer.feed("PING x\r\n");
  assert.deepEqual(lines, ["PING x"]);
});

test("rejects an embedded CR that is not part of CRLF", () => {
  const { framer } = collect();
  assert.throws(() => framer.feed("PRIVMSG bob :a\rb\r\n"), ProtocolError);
});

test("rejects a CR followed by CR instead of LF", () => {
  const { framer } = collect();
  assert.throws(() => framer.feed("NICK a\r\r\n"), ProtocolError);
});

test("rejects a NUL octet anywhere in the line", () => {
  const { framer } = collect();
  assert.throws(() => framer.feed("NICK al\0ice\r\n"), ProtocolError);
});

test("rejects lines longer than 512 octets", () => {
  const { framer } = collect();
  assert.throws(() => framer.feed(`${"A".repeat(MAX_LINE_LENGTH + 1)}\r\n`), ProtocolError);
});

test("accepts a line of exactly the maximum length", () => {
  const { lines, framer } = collect();
  framer.feed(`${"A".repeat(MAX_LINE_LENGTH)}\r\n`);
  assert.equal(lines[0].length, MAX_LINE_LENGTH);
});

test("accepts Buffer input and treats CRLF only as terminator", () => {
  const { lines, framer } = collect();
  framer.feed(Buffer.from("CAP LS 302\r\n", "utf8"));
  assert.deepEqual(lines, ["CAP LS 302"]);
});
