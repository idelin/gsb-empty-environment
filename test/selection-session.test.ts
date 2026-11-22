import test from "node:test";
import assert from "node:assert/strict";
import {
  BrowserTrust,
  FileObjectStore,
  FileTicket,
  ForeignTicketError,
  SelectionSession,
  TicketRevokedError,
  UnknownTicketError,
  UntrustedSelectionError,
} from "../src/index.ts";

const encoder = new TextEncoder();

function bytes(text: string): Uint8Array {
  return encoder.encode(text);
}

async function collect(stream: ReadableStream<Uint8Array>): Promise<Uint8Array> {
  const reader = stream.getReader();
  const chunks: Uint8Array[] = [];
  for (;;) {
    const { done, value } = await reader.read();
    if (done) break;
    chunks.push(value);
  }
  const extra = await reader.read();
  assert.equal(extra.done, true, "stream stays done after close");
  assert.equal(extra.value, undefined);
  const total = chunks.reduce((sum, chunk) => sum + chunk.length, 0);
  const out = new Uint8Array(total);
  let offset = 0;
  for (const chunk of chunks) {
    out.set(chunk, offset);
    offset += chunk.length;
  }
  return out;
}

function setup() {
  const store = new FileObjectStore();
  const trust = new BrowserTrust();
  const session = new SelectionSession({ documentId: "doc-1", store });
  return { store, trust, session };
}

test("multi-file selection mints tickets bound to document and object ids", async () => {
  const { store, trust, session } = setup();
  const idA = store.register("a.txt", bytes("alpha"));
  const idB = store.register("b.bin", bytes("bravo"));
  const idC = store.register("empty.txt", new Uint8Array(0));

  const tickets = session.applySelection(
    trust.selectionEvent([idA, idB, idC]),
  );

  assert.equal(tickets.length, 3);
  assert.equal(session.size, 3);
  assert.deepEqual(
    tickets.map((t) => t.objectId),
    [idA, idB, idC],
  );
  for (const ticket of tickets) {
    assert.equal(ticket.documentId, "doc-1");
    assert.ok(Object.isFrozen(ticket));
  }
  assert.equal(tickets[0].name, "a.txt");
  assert.equal(tickets[1].size, 5);

  assert.deepEqual(await collect(session.openReadStream(tickets[0])), bytes("alpha"));
  assert.deepEqual(await collect(session.openReadStream(tickets[1])), bytes("bravo"));
  // Empty file: stream ends immediately and cleanly.
  assert.deepEqual(await collect(session.openReadStream(tickets[2])), new Uint8Array(0));
});

test("path strings, plain data, and self-made tickets are not capabilities", () => {
  const { store, trust, session } = setup();
  const idA = store.register("a.txt", bytes("alpha"));
  session.applySelection(trust.selectionEvent([idA]));

  // A control's path string is not a capability.
  assert.throws(() => session.openReadStream("/home/user/a.txt"), UnknownTicketError);
  // Even a genuine object id string is not a capability.
  assert.throws(() => session.openReadStream(idA), UnknownTicketError);
  // Plain lookalike objects carry no capability either.
  assert.throws(
    () => session.openReadStream({ objectId: idA, documentId: "doc-1" }),
    UnknownTicketError,
  );
  // Scripts cannot mint their own tickets: the brand is module-private.
  assert.throws(
    () =>
      new FileTicket(Symbol("selection-session.ticket"), {
        objectId: idA,
        documentId: "doc-1",
        name: "a.txt",
        size: 5,
      }),
    UnknownTicketError,
  );
});

test("forged or replayed selection events are rejected", () => {
  const { store, trust, session } = setup();
  const idA = store.register("a.txt", bytes("alpha"));

  // A plain literal shaped like an event is not trusted.
  assert.throws(
    () => session.applySelection({ kind: "selection", objectIds: [idA] } as never),
    UntrustedSelectionError,
  );
  assert.throws(
    () => session.applySelection({ kind: "cancel" } as never),
    UntrustedSelectionError,
  );

  // A genuine event is single-use: replay is rejected.
  const event = trust.selectionEvent([idA]);
  session.applySelection(event);
  assert.throws(() => session.applySelection(event), UntrustedSelectionError);
});

test("re-selection replaces the set; stale async reads finish old file, never see new one", async () => {
  const { store, trust, session } = setup();
  const big = new Uint8Array(16 * 1024 * 3);
  for (let i = 0; i < big.length; i += 1) big[i] = i % 251;
  const idOld = store.register("old.bin", big);
  const idNew = store.register("new.bin", bytes("fresh"));

  const [oldTicket] = session.applySelection(trust.selectionEvent([idOld]));

  // An async read is already in flight when the user re-selects.
  const reader = session.openReadStream(oldTicket).getReader();
  const first = await reader.read();
  assert.equal(first.done, false);

  const [newTicket] = session.applySelection(trust.selectionEvent([idNew]));
  assert.equal(session.size, 1);
  assert.deepEqual(session.currentTickets(), [newTicket]);

  // The in-flight stream keeps delivering the OLD file to its end.
  const chunks: Uint8Array[] = [first.value!];
  for (;;) {
    const { done, value } = await reader.read();
    if (done) break;
    chunks.push(value);
  }
  const received = new Uint8Array(chunks.reduce((n, c) => n + c.length, 0));
  let offset = 0;
  for (const chunk of chunks) {
    received.set(chunk, offset);
    offset += chunk.length;
  }
  assert.deepEqual(received, big);

  // The stale ticket cannot be used again, and never yields the new file.
  assert.throws(() => session.openReadStream(oldTicket), TicketRevokedError);
  // The new ticket reads only the new file.
  assert.deepEqual(await collect(session.openReadStream(newTicket)), bytes("fresh"));
});

test("cancellation empties the set and revokes outstanding tickets", () => {
  const { store, trust, session } = setup();
  const idA = store.register("a.txt", bytes("alpha"));
  const [ticket] = session.applySelection(trust.selectionEvent([idA]));

  const result = session.applySelection(trust.cancelEvent());

  assert.deepEqual(result, []);
  assert.equal(session.size, 0);
  assert.deepEqual(session.currentTickets(), []);
  assert.throws(() => session.openReadStream(ticket), TicketRevokedError);
});

test("document navigation revokes all tickets; tickets are session-owned", async () => {
  const { store, trust, session } = setup();
  const idA = store.register("a.txt", bytes("alpha"));
  const [ticket] = session.applySelection(trust.selectionEvent([idA]));

  // A ticket is owned by its issuing session, not by any session.
  const other = new SelectionSession({ documentId: "doc-1", store });
  assert.throws(() => other.openReadStream(ticket), ForeignTicketError);

  session.navigateTo("doc-2");
  assert.equal(session.documentId, "doc-2");
  assert.equal(session.size, 0);
  assert.throws(() => session.openReadStream(ticket), TicketRevokedError);

  // After navigation the document can select again; new tickets work.
  const [fresh] = session.applySelection(trust.selectionEvent([idA]));
  assert.equal(fresh.documentId, "doc-2");
  assert.deepEqual(await collect(session.openReadStream(fresh)), bytes("alpha"));
});
