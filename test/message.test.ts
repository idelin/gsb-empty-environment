import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import test from "node:test";

import {
  canonicalizeMessage,
  computeIdFromCanonicalText,
  inspectMessageText,
  isCanonicalMessageText,
  MessageLog,
  serializeCanonical,
  serializeSigningPayload,
  type Message,
  type SignatureVerifier,
  type StoredMessage,
} from "../index.ts";

const AUTHOR = "@alice.ed25519";

const verify: SignatureVerifier = ({ message }) =>
  message.signature.startsWith("sig:valid");

function makeMessage(overrides: Partial<Message> = {}): Message {
  return {
    previous: null,
    sequence: 1,
    author: AUTHOR,
    timestamp: 1_700_000_000_000,
    hash: "sha256",
    content: { type: "post", text: "hello" },
    signature: "sig:valid:first",
    ...overrides,
  };
}

function wire(message: Message): string {
  return serializeCanonical(message);
}

function expectedId(message: Message): string {
  return `${createHash("sha256")
    .update(wire(message), "utf8")
    .digest("base64")}.sha256`;
}

function nextMessage(
  previous: StoredMessage,
  content: unknown,
  signature: string,
): Message {
  return makeMessage({
    previous: previous.id,
    sequence: previous.sequence + 1,
    content,
    signature,
  });
}

test("accepts canonical text and derives the ID from bytes including signature", () => {
  const message = makeMessage();
  const text = wire(message);
  const log = new MessageLog({ author: AUTHOR, verify });

  const result = log.ingest(text);

  assert.equal(result.status, "accepted");
  assert.equal(result.ok, true);
  assert.equal(result.id, expectedId(message));
  assert.equal(result.id, computeIdFromCanonicalText(text));
  assert.equal(log.length, 1);
  assert.equal(log.head?.canonical, text);

  const changedSignature = makeMessage({ signature: "sig:valid:other" });
  assert.notEqual(expectedId(changedSignature), expectedId(message));
});

test("UTF-8 content is hashed without an ASCII-only shortcut", () => {
  const message = makeMessage({
    content: { type: "post", text: "你好😀" },
  });
  const text = wire(message);

  assert.match(text, /你好😀/);
  assert.equal(
    computeIdFromCanonicalText(text),
    `${createHash("sha256")
      .update(Buffer.from(text, "utf8"))
      .digest("base64")}.sha256`,
  );
});

test("rejects a top-level key-order change", () => {
  const message = makeMessage();
  const reordered = JSON.stringify(
    {
      sequence: message.sequence,
      previous: message.previous,
      author: message.author,
      timestamp: message.timestamp,
      hash: message.hash,
      content: message.content,
      signature: message.signature,
    },
    null,
    2,
  );

  const result = new MessageLog({ author: AUTHOR, verify }).ingest(reordered);

  assert.equal(result.status, "rejected");
  assert.equal(result.reason, "NON_CANONICAL");
});

test("rejects whitespace changes while leaving JSON semantics intact", () => {
  const text = wire(makeMessage());
  const variants = [
    text.replace(/\n/g, "\r\n"),
    text.replaceAll("  ", "\t"),
    text.replace("{\n", "{ \n"),
    `${text}\n`,
  ];

  for (const variant of variants) {
    const result = new MessageLog({ author: AUTHOR, verify }).ingest(variant);
    assert.equal(result.status, "rejected");
    assert.equal(result.reason, "NON_CANONICAL");
  }
});

test("rejects missing and extra top-level fields", () => {
  const missing = makeMessage();
  delete (missing as Partial<Message>).hash;
  const extra = { ...makeMessage(), extra: true };

  for (const candidate of [missing, extra]) {
    const result = new MessageLog({ author: AUTHOR, verify }).ingest(
      JSON.stringify(candidate, null, 2),
    );
    assert.equal(result.status, "rejected");
    assert.equal(result.reason, "INVALID_MESSAGE");
  }
});

test("rejects invalid JSON and a non-genesis first message", () => {
  const malformed = new MessageLog({ author: AUTHOR, verify }).ingest("{");
  assert.equal(malformed.status, "rejected");
  assert.equal(malformed.reason, "INVALID_JSON");

  const noGenesis = new MessageLog({ author: AUTHOR, verify }).ingest(
    wire(makeMessage({ sequence: 2, previous: "other.sha256" })),
  );
  assert.equal(noGenesis.status, "rejected");
  assert.equal(noGenesis.reason, "SEQUENCE_GAP");
});

test("rejects a broken previous link, then still accepts the correct link", () => {
  const log = new MessageLog({ author: AUTHOR, verify });
  const first = log.ingest(wire(makeMessage()));
  assert.equal(first.status, "accepted");

  const broken = nextMessage(
    first.stored,
    { type: "post", text: "broken" },
    "sig:valid:broken",
  );
  broken.previous = "different.sha256";

  const rejected = log.ingest(wire(broken));
  assert.equal(rejected.status, "rejected");
  assert.equal(rejected.reason, "BROKEN_CHAIN");
  assert.equal(log.length, 1);
  assert.equal(log.isForked, false);

  const fixed = nextMessage(
    first.stored,
    { type: "post", text: "fixed" },
    "sig:valid:fixed",
  );
  assert.equal(log.ingest(wire(fixed)).status, "accepted");
});

test("rejects a skipped sequence, then accepts the next one", () => {
  const log = new MessageLog({ author: AUTHOR, verify });
  const first = log.ingest(wire(makeMessage()));
  assert.equal(first.status, "accepted");

  const gap = nextMessage(
    first.stored,
    { type: "post", text: "gap" },
    "sig:valid:gap",
  );
  gap.sequence = 3;

  const rejected = log.ingest(wire(gap));
  assert.equal(rejected.status, "rejected");
  assert.equal(rejected.reason, "SEQUENCE_GAP");
  assert.equal(log.length, 1);

  const second = nextMessage(
    first.stored,
    { type: "post", text: "second" },
    "sig:valid:second",
  );
  assert.equal(log.ingest(wire(second)).status, "accepted");
});

test("same sequence with a different ID quarantines the log and keeps both messages", () => {
  const log = new MessageLog({ author: AUTHOR, verify });
  const first = log.ingest(wire(makeMessage()));
  assert.equal(first.status, "accepted");

  const secondA = nextMessage(
    first.stored,
    { type: "post", text: "branch A" },
    "sig:valid:branch-a",
  );
  const acceptedA = log.ingest(wire(secondA));
  assert.equal(acceptedA.status, "accepted");

  const secondB = nextMessage(
    first.stored,
    { type: "post", text: "branch B" },
    "sig:valid:branch-b",
  );
  const fork = log.ingest(wire(secondB));

  assert.equal(fork.status, "rejected");
  assert.equal(fork.reason, "FORK");
  assert.notEqual(acceptedA.id, expectedId(secondB));
  assert.equal(log.isForked, true);
  assert.equal(log.length, 2);
  assert.equal(log.forkEvidence.length, 1);

  const evidence = log.forkEvidence[0];
  assert.equal(evidence.author, AUTHOR);
  assert.equal(evidence.sequence, 2);
  assert.equal(evidence.accepted.canonical, wire(secondA));
  assert.equal(evidence.conflicting.canonical, wire(secondB));
  assert.equal(evidence.accepted.id, acceptedA.id);
  assert.equal(evidence.conflicting.id, expectedId(secondB));
  assert.notEqual(evidence.accepted.id, evidence.conflicting.id);

  const laterOnA = nextMessage(
    acceptedA.stored,
    { type: "post", text: "later" },
    "sig:valid:later",
  );
  const later = log.ingest(wire(laterOnA));
  assert.equal(later.status, "rejected");
  assert.equal(later.reason, "AUTHOR_FORKED");
  assert.equal(log.length, 2);
});

test("an exact repeat is a duplicate, not a fork", () => {
  const text = wire(makeMessage());
  const log = new MessageLog({ author: AUTHOR, verify });

  const first = log.ingest(text);
  const second = log.ingest(text);

  assert.equal(first.status, "accepted");
  assert.equal(second.status, "duplicate");
  assert.equal(second.ok, true);
  assert.equal(second.id, first.id);
  assert.equal(log.length, 1);
  assert.equal(log.isForked, false);
  assert.equal(log.forkEvidence.length, 0);
});

test("rejects another author's otherwise valid message", () => {
  const message = makeMessage({ author: "@mallory.ed25519" });
  const result = new MessageLog({ author: AUTHOR, verify }).ingest(wire(message));

  assert.equal(result.status, "rejected");
  assert.equal(result.reason, "WRONG_AUTHOR");
});

test("rejects a failed injected signature check", () => {
  const message = makeMessage({ signature: "sig:bad" });
  const result = new MessageLog({ author: AUTHOR, verify }).ingest(wire(message));

  assert.equal(result.status, "rejected");
  assert.equal(result.reason, "INVALID_SIGNATURE");
});

test("the verifier receives the canonical text and the signature-free payload", () => {
  const message = makeMessage();
  let seen: { canonical: string; signingPayload: string } | undefined;
  const capture: SignatureVerifier = (input) => {
    seen = {
      canonical: input.canonical,
      signingPayload: input.signingPayload,
    };
    return true;
  };

  inspectMessageText(wire(message), capture);

  assert.equal(seen?.canonical, wire(message));
  assert.equal(seen?.signingPayload, serializeSigningPayload(message));
  assert.equal(seen?.canonical.includes('"signature": "sig:valid:first"'), true);
  assert.equal(seen?.signingPayload.includes('"signature"'), false);
});

test("canonical helpers agree and reject non-canonical strings", () => {
  const message = makeMessage();
  const text = wire(message);

  assert.equal(isCanonicalMessageText(text), true);
  assert.equal(isCanonicalMessageText(`${text}\n`), false);
  assert.equal(canonicalizeMessage(message).canonical, text);
  assert.equal(canonicalizeMessage(text).canonical, text);
  assert.equal(
    computeIdFromCanonicalText(canonicalizeMessage(message).canonical),
    expectedId(message),
  );
});
