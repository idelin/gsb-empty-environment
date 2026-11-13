import assert from "node:assert/strict";
import { test } from "node:test";

import {
  SFrameAuthenticationError,
  SFrameFrameAdmission,
  SFrameKIDRegistry,
  SFrameNonMinimalEncodingError,
  SFrameReplayError,
  SFrameSenderMismatchError,
  SFrameTruncatedHeaderError,
  SFrameUnregisteredKIDError,
  checkSFrameHeader,
  createSFrameAdmissionTransform,
  encodeSFrameHeader,
  parseSFrameHeader,
} from "../index.ts";
import type { SFrameAuthenticatedDecryptor } from "../index.ts";

function frameBytes(kid: number | bigint, ctr: number | bigint, payload = []) {
  const header = encodeSFrameHeader(kid, ctr);
  return Uint8Array.from([...header, ...payload]);
}

function makeAdmission(
  decrypt: SFrameAuthenticatedDecryptor = () => true,
) {
  const registry = new SFrameKIDRegistry();
  registry.register({ trackId: "audio", kid: 1, senderId: "alice" });
  registry.register({ trackId: "audio", kid: 2, senderId: "alice" });
  return new SFrameFrameAdmission({
    registry,
    trackId: "audio",
    senderId: "alice",
    decrypt,
  });
}

test("RFC 9605 compact header vectors and fields", () => {
  const zero = parseSFrameHeader(Uint8Array.from([0x00]));
  assert.equal(zero.x, false);
  assert.equal(zero.k, 0);
  assert.equal(zero.y, false);
  assert.equal(zero.c, 0);
  assert.equal(zero.kid, 0);
  assert.equal(zero.ctr, 0);
  assert.equal(zero.headerLength, 1);
  assert.equal(zero.payloadOffset, 1);

  const one = parseSFrameHeader(Uint8Array.from([0x01]));
  assert.equal(one.kid, 0);
  assert.equal(one.ctr, 1);

  assert.deepEqual(
    Array.from(encodeSFrameHeader(0, 1)),
    [0x01],
  );
  assert.deepEqual(
    Array.from(encodeSFrameHeader(8, 0)),
    [0x80, 0x08],
  );
  assert.deepEqual(
    Array.from(encodeSFrameHeader(0, 8)),
    [0x08, 0x08],
  );
  assert.deepEqual(
    Array.from(encodeSFrameHeader(8, 8)),
    [0x88, 0x08, 0x08],
  );
  assert.deepEqual(
    Array.from(encodeSFrameHeader(0x123, 0x456)),
    [0x99, 0x01, 0x23, 0x04, 0x56],
  );

  const x123 = parseSFrameHeader(
    Uint8Array.from([0x99, 0x01, 0x23, 0x04, 0x56]),
  );
  assert.equal(x123.x, true);
  assert.equal(x123.k, 1);
  assert.equal(x123.y, true);
  assert.equal(x123.c, 1);
  assert.equal(x123.kid, 0x123);
  assert.equal(x123.ctr, 0x456);
  assert.equal(x123.kidLength, 2);
  assert.equal(x123.ctrLength, 2);
});

test("all compact integer widths round-trip canonically", () => {
  const values = [
    0n,
    1n,
    7n,
    8n,
    0x100n,
    0xffffn,
    0x10000n,
    0x123456789an,
    (1n << 53n) + 1n,
    (1n << 64n) - 1n,
  ];

  for (const kid of values) {
    for (const ctr of values) {
      const header = encodeSFrameHeader(kid, ctr);
      const parsed = parseSFrameHeader(header);
      assert.equal(parsed.kidBigInt, kid);
      assert.equal(parsed.ctrBigInt, ctr);
      assert.deepEqual(
        Array.from(encodeSFrameHeader(parsed.kidBigInt, parsed.ctrBigInt)),
        Array.from(header),
      );
    }
  }
});

test("non-minimal KID encodings are rejected", () => {
  for (const bytes of [
    [0x80, 0x00],
    [0x80, 0x07],
    [0x81, 0x00, 0x08],
    [0x82, 0x00, 0x01, 0x00],
  ]) {
    assert.throws(
      () => parseSFrameHeader(Uint8Array.from(bytes)),
      (error: unknown) => {
        assert.ok(error instanceof SFrameNonMinimalEncodingError);
        assert.equal(error.field, "KID");
        assert.match(error.message, /non-minimal/i);
        return true;
      },
    );
  }
});

test("non-minimal CTR encodings are rejected", () => {
  for (const bytes of [
    [0x08, 0x00],
    [0x08, 0x07],
    [0x09, 0x00, 0x08],
    [0x0a, 0x00, 0x01, 0x00],
  ]) {
    assert.throws(
      () => parseSFrameHeader(Uint8Array.from(bytes)),
      (error: unknown) => {
        assert.ok(error instanceof SFrameNonMinimalEncodingError);
        assert.equal(error.field, "CTR");
        return true;
      },
    );
  }
});

test("truncated headers are rejected", () => {
  for (const bytes of [
    [],
    [0x80],
    [0x08],
    [0x88, 0x08],
    [0x90, 0x01],
  ]) {
    assert.throws(
      () => parseSFrameHeader(Uint8Array.from(bytes)),
      SFrameTruncatedHeaderError,
    );
  }
});

test("byte offsets and trailing payload are handled", () => {
  const encoded = encodeSFrameHeader(0x123, 0x456);
  const source = Uint8Array.from([0xaa, 0xbb, ...encoded, 0xcc, 0xdd]);
  const parsed = parseSFrameHeader(source, { byteOffset: 2 });
  assert.equal(parsed.kid, 0x123);
  assert.equal(parsed.ctr, 0x456);
  assert.equal(parsed.payloadOffset, 7);
  assert.deepEqual(Array.from(parsed.headerBytes), Array.from(encoded));
});

test("registry binds a KID to one sender on one track", () => {
  const registry = new SFrameKIDRegistry();
  registry.register({ trackId: "video", kid: 3, senderId: "alice" });
  registry.register({ trackId: "audio", kid: 3, senderId: "bob" });
  registry.register({ trackId: "video", kid: 3, senderId: "alice" });

  assert.equal(registry.senderFor("video", 3), "alice");
  assert.equal(registry.senderFor("audio", 3), "bob");
  assert.equal(registry.size, 2);

  assert.throws(
    () => registry.register({ trackId: "video", kid: 3, senderId: "bob" }),
    /already registered/i,
  );

  registry.assertOwnedBy("video", 3, "alice");
  assert.throws(
    () => registry.assertOwnedBy("video", 3, "bob"),
    SFrameSenderMismatchError,
  );
  assert.throws(
    () => registry.assertOwnedBy("video", 4, "alice"),
    SFrameUnregisteredKIDError,
  );

  assert.equal(registry.unregister("audio", 3), true);
  assert.equal(registry.has("audio", 3), false);

  const globalRegistry = new SFrameKIDRegistry();
  globalRegistry.register(9, "carol");
  assert.equal(globalRegistry.senderFor("any-track", 9), "carol");
  globalRegistry.register({ trackId: "any-track", kid: 9, senderId: "dave" });
  assert.equal(globalRegistry.senderFor("any-track", 9), "dave");
  assert.equal(globalRegistry.senderFor("other-track", 9), "carol");
});

test("unregistered and sender-mismatched KIDs never reach decryption", async () => {
  let calls = 0;
  const admission = makeAdmission(() => {
    calls += 1;
    return true;
  });

  await assert.rejects(
    admission.admit(frameBytes(99, 1)),
    SFrameUnregisteredKIDError,
  );
  assert.equal(calls, 0);

  const bobRegistry = new SFrameKIDRegistry();
  bobRegistry.register({ trackId: "audio", kid: 1, senderId: "alice" });
  const bobAdmission = new SFrameFrameAdmission({
    registry: bobRegistry,
    trackId: "audio",
    senderId: "bob",
    decrypt: () => {
      calls += 1;
      return true;
    },
  });
  await assert.rejects(
    bobAdmission.admit(frameBytes(1, 1)),
    SFrameSenderMismatchError,
  );
  assert.equal(calls, 0);
});

test("CTR must strictly increase for each KID", async () => {
  const admission = makeAdmission();

  await admission.admit(frameBytes(1, 5));
  await assert.rejects(admission.admit(frameBytes(1, 5)), SFrameReplayError);
  await assert.rejects(admission.admit(frameBytes(1, 4)), SFrameReplayError);
  await admission.admit(frameBytes(1, 6));

  // A different KID has an independent counter.
  await admission.admit(frameBytes(2, 5));
  await admission.admit(frameBytes(2, 6));

  assert.equal(admission.lastAcceptedCtr(1), 6);
  assert.equal(admission.lastAcceptedCtr(2), 6);
});

test("authentication failure does not advance replay state", async () => {
  let accepted = false;
  const admission = makeAdmission(() => accepted);

  await assert.rejects(
    admission.admit(frameBytes(1, 5)),
    SFrameAuthenticationError,
  );
  assert.equal(admission.lastAcceptedCtr(1), undefined);

  accepted = true;
  await admission.admit(frameBytes(1, 5));
  assert.equal(admission.lastAcceptedCtr(1), 5);
});

test("decryptor exceptions are converted to authentication failures", async () => {
  const admission = makeAdmission(() => {
    throw new Error("bad tag");
  });

  await assert.rejects(
    admission.admit(frameBytes(1, 1)),
    (error: unknown) => {
      assert.ok(error instanceof SFrameAuthenticationError);
      assert.match(error.message, /decryptor failed/i);
      return true;
    },
  );
  assert.equal(admission.lastAcceptedCtr(1), undefined);
});

test("decrypt context exposes AAD header and payload", async () => {
  let observed: {
    kid: bigint;
    ctr: bigint;
    header: Uint8Array;
    payload: Uint8Array;
    sender: string;
  } | undefined;

  const admission = makeAdmission((context) => {
    observed = {
      kid: context.kidBigInt,
      ctr: context.ctrBigInt,
      header: Uint8Array.from(context.headerBytes),
      payload: Uint8Array.from(context.payload),
      sender: context.senderId,
    };
    return true;
  });

  await admission.admit(frameBytes(1, 2, [0xde, 0xad]));
  assert.deepEqual(observed, {
    kid: 1n,
    ctr: 2n,
    header: encodeSFrameHeader(1, 2),
    payload: Uint8Array.from([0xde, 0xad]),
    sender: "alice",
  });
});

test("decryptor may return replacement frame bytes", async () => {
  const admission = makeAdmission(() => Uint8Array.from([0x99, 0x88]));
  const output = await admission.admit(frameBytes(1, 1, [0xaa]));
  assert.deepEqual(
    Array.from(
      output instanceof Uint8Array
        ? output
        : new Uint8Array(output.data),
    ),
    [0x99, 0x88],
  );
});

test("concurrent frames for one KID are serialized and replayed once", async () => {
  let calls = 0;
  const admission = makeAdmission(async () => {
    calls += 1;
    await new Promise((resolve) => setTimeout(resolve, 5));
    return true;
  });

  const results = await Promise.allSettled([
    admission.admit(frameBytes(1, 10)),
    admission.admit(frameBytes(1, 10)),
  ]);
  assert.equal(results.filter((result) => result.status === "fulfilled").length, 1);
  assert.equal(
    results.filter(
      (result) =>
        result.status === "rejected" &&
        result.reason instanceof SFrameReplayError,
    ).length,
    1,
  );
  assert.equal(calls, 1);
});

test("checkSFrameHeader applies registry and replay checks without committing", () => {
  const registry = new SFrameKIDRegistry();
  registry.register({ trackId: "video", kid: 7, senderId: "alice" });

  const header = checkSFrameHeader(frameBytes(7, 3), {
    registry,
    trackId: "video",
    senderId: "alice",
  });
  assert.equal(header.kid, 7);
  assert.equal(header.ctr, 3);
});

test("transform drops rejected frames and passes valid frames", async () => {
  const admission = makeAdmission();
  const stream = createSFrameAdmissionTransform(admission);
  const writer = stream.writable.getWriter();
  const reader = stream.readable.getReader();

  const valid = frameBytes(1, 1, [0xaa]);
  const firstRead = reader.read();
  await writer.write(frameBytes(99, 1));
  await writer.write(valid);

  const result = await firstRead;
  assert.equal(result.done, false);
  assert.equal(result.value, valid);

  await writer.close();
  const end = await reader.read();
  assert.equal(end.done, true);
});

test("transform can error the stream instead of dropping", async () => {
  const admission = new SFrameFrameAdmission({
    registry: new SFrameKIDRegistry(),
    trackId: "audio",
    senderId: "alice",
    decrypt: () => true,
    rejectBehavior: "throw",
  });
  const stream = admission.createTransform();
  const writer = stream.writable.getWriter();
  const reader = stream.readable.getReader();
  const readAssertion = assert.rejects(
    reader.read(),
    SFrameUnregisteredKIDError,
  );

  await writer.write(frameBytes(1, 1));
  await readAssertion;
});
