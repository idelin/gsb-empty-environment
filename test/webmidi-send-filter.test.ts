import { test } from "node:test";
import assert from "node:assert/strict";

import {
  MidiByteStreamFilter,
  MidiSendRejectedError,
  SysexPortRegistry,
  createMidiDocumentSession,
  createWebMidiSendFilter,
  filterMidiBytes,
} from "../index.ts";

function recordingOutput(id = "device-1") {
  const sent: number[][] = [];
  return {
    id,
    sent,
    send(data: number[] | Uint8Array) {
      sent.push(Array.from(data));
    },
  };
}

test("unauthorized sysex is removed across split writes", () => {
  const output = recordingOutput();
  const filter = createWebMidiSendFilter(output, {
    sysexEnabled: false,
    documentId: "attacker",
    registry: new SysexPortRegistry(),
    releaseOnPageHide: false,
  });

  assert.deepEqual(
    filter.send([0x90, 0x3c, 0x40, 0xf0, 0x7d, 0x01]),
    [0x90, 0x3c, 0x40],
  );
  assert.equal(filter.inSysEx, true);

  assert.deepEqual(filter.send([0x02, 0xf8, 0x03, 0x04]), [0xf8]);
  assert.equal(filter.inSysEx, true);

  assert.deepEqual(
    filter.send([0x05, 0xf7, 0x90, 0x3e, 0x40]),
    [0x90, 0x3e, 0x40],
  );
  assert.equal(filter.inSysEx, false);

  assert.deepEqual(output.sent, [
    [0x90, 0x3c, 0x40],
    [0xf8],
    [0x90, 0x3e, 0x40],
  ]);
});

test("running status and realtime bytes survive outside sysex", () => {
  const output = recordingOutput();
  const filter = createWebMidiSendFilter(output, {
    sysexEnabled: false,
    documentId: "normal-sender",
    registry: new SysexPortRegistry(),
    releaseOnPageHide: false,
  });

  assert.deepEqual(filter.send([0x90, 0x3c, 0x40]), [0x90, 0x3c, 0x40]);
  assert.equal(filter.runningStatus, 0x90);

  assert.deepEqual(filter.send([0xf8, 0x3e, 0x40]), [0xf8, 0x3e, 0x40]);
  assert.equal(filter.runningStatus, 0x90);

  assert.deepEqual(
    filter.send([0xb0, 0x07, 0x7f, 0xfe]),
    [0xb0, 0x07, 0x7f, 0xfe],
  );
  assert.equal(filter.runningStatus, 0xb0);
  assert.deepEqual(output.sent, [
    [0x90, 0x3c, 0x40],
    [0xf8, 0x3e, 0x40],
    [0xb0, 0x07, 0x7f, 0xfe],
  ]);
});

test("realtime bytes inside sysex do not end the blocked region", () => {
  const stream = new MidiByteStreamFilter();

  assert.deepEqual(stream.filter([0xf0, 0xf8, 0x7d]), [0xf8]);
  assert.equal(stream.inSysEx, true);
  assert.deepEqual(stream.filter([0xff, 0x01, 0xf8]), [0xff, 0xf8]);
  assert.equal(stream.inSysEx, true);
  assert.deepEqual(stream.filter([0xf7]), []);
  assert.equal(stream.inSysEx, false);

  const strict = new MidiByteStreamFilter({
    passRealtimeDuringSysex: false,
  });
  assert.deepEqual(strict.filter([0xf0, 0xf8, 0x01, 0xf7]), []);
});

test("one authorized document exclusively owns a sysex port", () => {
  const registry = new SysexPortRegistry();
  const outputA = recordingOutput();
  const outputB = recordingOutput();
  const outputC = recordingOutput();

  const filterA = createWebMidiSendFilter(outputA, {
    sysexEnabled: true,
    documentId: "document-a",
    registry,
    releaseOnPageHide: false,
  });
  const filterB = createWebMidiSendFilter(outputB, {
    sysexEnabled: true,
    documentId: "document-b",
    registry,
    releaseOnPageHide: false,
  });
  const filterC = createWebMidiSendFilter(outputC, {
    sysexEnabled: false,
    documentId: "document-c",
    registry,
    releaseOnPageHide: false,
  });

  assert.equal(filterA.ownsSysexPort, true);
  assert.equal(filterB.ownsSysexPort, false);

  assert.deepEqual(
    filterA.send([0xf0, 0x7d, 0x01, 0xf7]),
    [0xf0, 0x7d, 0x01, 0xf7],
  );
  assert.deepEqual(outputA.sent, [[0xf0, 0x7d, 0x01, 0xf7]]);
  assert.deepEqual(outputC.sent, []);

  assert.throws(
    () => filterB.send([0x90, 0x3c, 0x40]),
    (error: unknown) =>
      error instanceof MidiSendRejectedError &&
      error.reason === "sysex-port-owned-by-another-document" &&
      error.ownerDocumentId === "document-a",
  );
  assert.deepEqual(outputB.sent, []);
  assert.throws(
    () => filterC.send([0x90, 0x3c, 0x40, 0xf0, 0x01, 0xf7]),
    (error: unknown) =>
      error instanceof MidiSendRejectedError &&
      error.reason === "sysex-port-owned-by-another-document",
  );
  assert.deepEqual(outputC.sent, []);

  filterA.release();
  assert.equal(registry.hasOwner("device-1"), false);

  assert.deepEqual(
    filterC.send([0x90, 0x3c, 0x40, 0xf0, 0x01, 0xf7]),
    [0x90, 0x3c, 0x40],
  );
  assert.deepEqual(filterB.send([0xf0, 0x01, 0xf7]), [0xf0, 0x01, 0xf7]);
  assert.equal(filterB.ownsSysexPort, true);
  assert.equal(registry.ownerOf("device-1"), "document-b");

  assert.throws(
    () => filterA.send([0x90, 0x3c, 0x40]),
    (error: unknown) =>
      error instanceof MidiSendRejectedError &&
      error.reason === "document-session-released",
  );
});

test("navigation releases the document's exclusive sysex claim", () => {
  const registry = new SysexPortRegistry();
  const pageA = new EventTarget();
  const pageB = new EventTarget();
  const outputA = recordingOutput();
  const outputB = recordingOutput();

  const sessionA = createMidiDocumentSession({
    documentId: "page-a",
    registry,
    lifecycleTarget: pageA,
  });
  const sessionB = createMidiDocumentSession({
    documentId: "page-b",
    registry,
    lifecycleTarget: pageB,
  });
  const filterA = sessionA.createFilter(outputA, { sysexEnabled: true });
  const filterB = sessionB.createFilter(outputB, { sysexEnabled: true });

  assert.equal(registry.ownerOf("device-1"), "page-a");
  assert.equal(sessionA.closed, false);
  sessionB.createFilter(recordingOutput("device-2"), {
    sysexEnabled: true,
  });
  assert.equal(registry.ownerOf("device-2"), "page-b");

  pageA.dispatchEvent(new Event("pagehide"));

  assert.equal(sessionA.closed, true);
  assert.equal(registry.hasOwner("device-1"), false);
  assert.deepEqual(
    sessionB.createFilter(recordingOutput("device-1"), {
      sysexEnabled: true,
    }).send([0xf0, 0x01, 0xf7]),
    [0xf0, 0x01, 0xf7],
  );

  assert.throws(
    () => filterA.send([0x90, 0x3c, 0x40]),
    (error: unknown) =>
      error instanceof MidiSendRejectedError &&
      error.reason === "document-session-released",
  );
  assert.throws(
    () => sessionA.createFilter(recordingOutput(), { sysexEnabled: true }),
    MidiSendRejectedError,
  );

  assert.deepEqual(filterB.send([0xf0, 0x02, 0xf7]), [0xf0, 0x02, 0xf7]);
});

test("unauthorized documents may share a port for non-sysex messages", () => {
  const registry = new SysexPortRegistry();
  const first = recordingOutput();
  const second = recordingOutput();

  const filterA = createWebMidiSendFilter(first, {
    sysexEnabled: false,
    documentId: "document-a",
    registry,
    releaseOnPageHide: false,
  });
  const filterB = createWebMidiSendFilter(second, {
    sysexEnabled: false,
    documentId: "document-b",
    registry,
    releaseOnPageHide: false,
  });

  assert.deepEqual(filterA.send([0x90, 0x3c, 0x40]), [0x90, 0x3c, 0x40]);
  assert.deepEqual(filterB.send([0xb0, 0x07, 0x7f]), [0xb0, 0x07, 0x7f]);
  assert.equal(registry.hasOwner("device-1"), false);
});

test("one-off filtering accepts arrays and validates byte ranges", () => {
  assert.deepEqual(
    filterMidiBytes(new Uint8Array([0x90, 0x3c, 0x40])),
    [0x90, 0x3c, 0x40],
  );
  assert.deepEqual(filterMidiBytes([0xf0, 0x01, 0xf7]), []);
  assert.throws(() => filterMidiBytes([0x100]), RangeError);
});
