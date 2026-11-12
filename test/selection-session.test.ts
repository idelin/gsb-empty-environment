import assert from 'node:assert/strict';
import { describe, it, beforeEach } from 'node:test';
import {
  InMemoryFileStorage,
  PathAccessError,
  SelectionSession,
  TicketConsumedError,
  TicketRevokedError,
  TrustedFilePicker,
  UntrustedEventError,
  createFileObject,
  type ReadonlyFileTicket,
  type SelectionChangeEvent,
  type StoredFileObject,
} from '../index.ts';

const DOCUMENT_A = 'doc-a';
const DOCUMENT_B = 'doc-b';

function makeFile(objectId: string, name: string, bytes: Uint8Array): StoredFileObject {
  return createFileObject(
    {
      objectId,
      name,
      size: bytes.length,
      lastModified: 1_700_000_000_000,
    },
    bytes,
  );
}

async function readAll(stream: ReadableStream<Uint8Array>): Promise<Uint8Array> {
  const reader = stream.getReader();
  const chunks: Uint8Array[] = [];
  let total = 0;
  for (;;) {
    const { done, value } = await reader.read();
    if (done) {
      break;
    }
    chunks.push(value);
    total += value.length;
  }
  reader.releaseLock();
  const merged = new Uint8Array(total);
  let offset = 0;
  for (const chunk of chunks) {
    merged.set(chunk, offset);
    offset += chunk.length;
  }
  return merged;
}

function bytes(text: string): Uint8Array {
  return new TextEncoder().encode(text);
}

function first<T>(items: readonly T[]): T {
  const item = items[0];
  assert.ok(item !== undefined, 'expected at least one ticket');
  return item;
}

describe('trusted file selection sessions', () => {
  let storage: InMemoryFileStorage;
  let session: SelectionSession;
  let picker: TrustedFilePicker;
  let fileA: StoredFileObject;
  let fileB: StoredFileObject;

  beforeEach(() => {
    storage = new InMemoryFileStorage();
    fileA = makeFile('obj-a', 'a.txt', bytes('alpha content'));
    fileB = makeFile('obj-b', 'b.txt', bytes('bravo content'));
    storage.add(fileA);
    storage.add(fileB);
    session = new SelectionSession(DOCUMENT_A, storage);
    picker = new TrustedFilePicker();
  });

  it('mints one ticket per file for a multi-file trusted selection', () => {
    const events: SelectionChangeEvent[] = [];
    session.addEventListener('selection', (event) => events.push(event));

    picker.confirmSelection(session, [fileA, fileB]);

    assert.equal(events.length, 1);
    const event = events[0];
    assert.equal(event.isTrusted, true);
    assert.equal(event.tickets.length, 2);
    assert.deepEqual(
      event.tickets.map((ticket) => ticket.objectId).sort(),
      ['obj-a', 'obj-b'],
    );
    for (const ticket of event.tickets) {
      assert.equal(ticket.documentId, DOCUMENT_A);
      assert.equal(ticket.revoked, false);
      assert.match(ticket.token, /^ft_[0-9a-f]{64}$/);
    }
    assert.equal(session.currentTickets.length, 2);
  });

  it('only a ticket can open a read stream, and the stream reaches the end', async () => {
    picker.confirmSelection(session, [fileA]);
    const ticket = first(session.currentTickets);

    const stream = session.openReadStream(ticket);
    const output = await readAll(stream);
    assert.deepEqual(output, fileA.bytes);

    assert.throws(() => stream.getReader(), /locked/);
  });

  it('never accepts a path string or object id as a file capability', () => {
    picker.confirmSelection(session, [fileA]);
    const ticket = first(session.currentTickets);

    const forgedToken = { ...ticket, token: 'obj-a' } as ReadonlyFileTicket;
    assert.throws(() => session.openReadStream(forgedToken), TicketRevokedError);

    const guessedToken = { ...ticket, token: `ft_${'0'.repeat(64)}` } as ReadonlyFileTicket;
    assert.throws(() => session.openReadStream(guessedToken), TicketRevokedError);

    const otherDocument = new SelectionSession(DOCUMENT_B, storage);
    assert.throws(() => otherDocument.openReadStream(ticket), TicketRevokedError);

    assert.throws(
      () => session.openReadStream({ ...ticket, token: fileA.descriptor.objectId }),
      TicketRevokedError,
    );
  });

  it('tickets are single-use capabilities', () => {
    picker.confirmSelection(session, [fileA]);
    const ticket = first(session.currentTickets);

    session.openReadStream(ticket);
    assert.throws(() => session.openReadStream(ticket), TicketConsumedError);
  });

  it('rejects synthetic, untrusted events without minting tickets', () => {
    assert.throws(
      () =>
        session.handleTrustedPickerEvent({
          kind: 'selection',
          selectionId: 1,
          files: [fileA],
        }),
      UntrustedEventError,
    );
    assert.equal(session.currentTickets.length, 0);
    assert.equal(session.selectionId, 0);
  });

  it('cancel keeps the existing set and emits no new tickets', () => {
    picker.confirmSelection(session, [fileA]);
    const firstTicket = first(session.currentTickets);

    let cancelled = false;
    session.addEventListener('cancel', (event) => {
      cancelled = true;
      assert.equal(event.isTrusted, true);
    });
    picker.cancel(session);

    assert.equal(cancelled, true);
    assert.equal(session.currentTickets.length, 1);
    assert.equal(first(session.currentTickets).token, firstTicket.token);
    assert.equal(firstTicket.revoked, false);
  });

  it('re-selection replaces the set and revokes every previous ticket', () => {
    picker.confirmSelection(session, [fileA]);
    const firstTickets = session.currentTickets;

    picker.confirmSelection(session, [fileB]);
    const secondTickets = session.currentTickets;

    assert.equal(secondTickets.length, 1);
    assert.equal(first(secondTickets).objectId, 'obj-b');
    assert.equal(session.currentTickets.length, 1);

    for (const stale of firstTickets) {
      assert.equal(stale.revoked, true);
      assert.throws(() => session.openReadStream(stale), TicketRevokedError);
    }
  });

  it('an old async callback holding a stale ticket never reaches new files', async () => {
    picker.confirmSelection(session, [fileA]);
    const staleTicket = first(session.currentTickets);

    let delayedFailure: unknown;
    const oldCallback = async (): Promise<void> => {
      await new Promise((resolve) => setTimeout(resolve, 0));
      try {
        await readAll(session.openReadStream(staleTicket));
      } catch (error) {
        delayedFailure = error;
      }
    };

    const pending = oldCallback();
    picker.confirmSelection(session, [fileB]);
    await pending;

    assert.ok(delayedFailure instanceof TicketRevokedError);

    const freshTicket = first(session.currentTickets);
    const freshBytes = await readAll(session.openReadStream(freshTicket));
    assert.deepEqual(freshBytes, fileB.bytes);

    const staleStreamAttempt = (): void => {
      session.openReadStream(staleTicket);
    };
    assert.throws(staleStreamAttempt, TicketRevokedError);
  });

  it('document navigation revokes tickets and terminates in-flight streams', async () => {
    const large = makeFile(
      'obj-large',
      'large.bin',
      new Uint8Array(200_000).fill(7),
    );
    storage.add(large);
    picker.confirmSelection(session, [large]);
    const ticket = first(session.currentTickets);

    const stream = session.openReadStream(ticket);
    const reader = stream.getReader();
    const firstChunk = await reader.read();
    assert.equal(firstChunk.done, false);

    const navigateEvent = session.navigate(DOCUMENT_B);
    assert.equal(navigateEvent.fromDocumentId, DOCUMENT_A);
    assert.equal(navigateEvent.toDocumentId, DOCUMENT_B);
    assert.equal(ticket.revoked, true);

    await assert.rejects(reader.read(), (error: unknown) => {
      assert.ok(error instanceof TicketRevokedError);
      return true;
    });
    reader.releaseLock();

    assert.throws(() => session.openReadStream(ticket), TicketRevokedError);
  });

  it('disposal revokes all tickets and blocks further selection events', () => {
    picker.confirmSelection(session, [fileA, fileB]);
    const tickets = session.currentTickets;

    let disposed = false;
    session.addEventListener('dispose', () => {
      disposed = true;
    });
    session.dispose();

    assert.equal(disposed, true);
    for (const ticket of tickets) {
      assert.equal(ticket.revoked, true);
    }
    assert.throws(() => picker.confirmSelection(session, [fileA]), /disposed/);
  });

  it('binds each ticket to exactly one document across parallel sessions', () => {
    const sessionB = new SelectionSession(DOCUMENT_B, storage);
    picker.confirmSelection(session, [fileA]);
    picker.confirmSelection(sessionB, [fileB]);

    const ticketA = first(session.currentTickets);
    const ticketB = first(sessionB.currentTickets);

    assert.equal(ticketA.documentId, DOCUMENT_A);
    assert.equal(ticketB.documentId, DOCUMENT_B);
    assert.throws(() => sessionB.openReadStream(ticketA), TicketRevokedError);
    assert.throws(() => session.openReadStream(ticketB), TicketRevokedError);
  });

  it('PathAccessError exists for hosts that reject raw path lookups explicitly', () => {
    const error = new PathAccessError();
    assert.ok(error instanceof Error);
    assert.match(error.message, /path strings are not file capabilities/);
  });
});
