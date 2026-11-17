/**
 * Navigation-race tests for the secure message receiver.
 *
 * Every test drives the platform's controllable event loop explicitly so
 * receive / validate / reply can be interleaved with navigation in exactly
 * the order an attacker would try to exploit.
 */

import test from 'node:test';
import assert from 'node:assert/strict';
import { Platform, PlatformMessageEvent } from '../src/platform.ts';
import { SecureMessageReceiver } from '../src/secure-receiver.ts';
import type { SecureMessage } from '../src/secure-receiver.ts';

const HOST = 'https://host.example';
const SENDER = 'https://sender.example';

function setup() {
  const platform = new Platform();
  const host = platform.createWindow('host', `${HOST}/app`);
  const sender = platform.createWindow('sender', `${SENDER}/app`);
  const received: Array<SecureMessage> = [];
  const receiver = new SecureMessageReceiver(
    platform,
    host,
    { allowedOrigins: [SENDER] },
    (message) => received.push(message),
  );
  return { platform, host, sender, received, receiver };
}

test('reply within the same document generation is delivered', () => {
  const { platform, host, sender, received } = setup();
  const senderInbox: Array<unknown> = [];
  sender.addMessageListener((event) => senderInbox.push(event.data));

  platform.postMessage(sender, host, { question: 1 }, HOST);
  platform.eventLoop.flush();
  assert.equal(received.length, 1);

  assert.equal(received[0].reply({ answer: 42 }), true);
  platform.eventLoop.flush();
  assert.deepEqual(senderInbox, [{ answer: 42 }]);
});

test('reply after same-origin navigation is dropped, never leaked to the new document', () => {
  const { platform, host, sender, received } = setup();

  platform.postMessage(sender, host, { question: 1 }, HOST);
  platform.eventLoop.flush();
  assert.equal(received.length, 1);

  // Sender window navigates: same Window object, brand-new document.
  const leaked: Array<unknown> = [];
  sender.navigate(`${SENDER}/taken-over`);
  sender.addMessageListener((event) => leaked.push(event.data));

  assert.equal(received[0].reply({ secret: 'for-the-original-document-only' }), false);
  platform.eventLoop.flush();
  assert.deepEqual(leaked, []);
});

test('reply after cross-origin navigation is dropped', () => {
  const { platform, host, sender, received } = setup();

  platform.postMessage(sender, host, { question: 1 }, HOST);
  platform.eventLoop.flush();

  const leaked: Array<unknown> = [];
  sender.navigate('https://attacker.example/');
  sender.addMessageListener((event) => leaked.push(event.data));

  assert.equal(received[0].reply({ secret: true }), false);
  platform.eventLoop.flush();
  assert.deepEqual(leaked, []);
});

test('receive/validate/reply interleaved with navigation never leaks to the new document', () => {
  const { platform, host, sender, received } = setup();

  // The original document sends two messages before the host processes any.
  platform.postMessage(sender, host, { n: 1 }, HOST);
  platform.postMessage(sender, host, { n: 2 }, HOST);

  // Host receives and validates the first message.
  assert.equal(platform.eventLoop.runNextTask(), true);
  assert.equal(received.length, 1);
  assert.deepEqual(received[0].data, { n: 1 });

  // Attacker navigates the sender window before the host replies and before
  // the second message is delivered. The new document listens for leaks.
  const newDocumentInbox: Array<unknown> = [];
  sender.navigate(`${SENDER}/taken-over`);
  sender.addMessageListener((event) => newDocumentInbox.push(event.data));

  // The second message still arrives with the *original* document's trusted
  // metadata, so validation sees the send-time origin, not the new document.
  assert.equal(platform.eventLoop.runNextTask(), true);
  assert.equal(received.length, 2);
  assert.equal(received[1].meta.origin, SENDER);

  // Both reply capabilities were issued for the dead generation: dropped.
  assert.equal(received[0].reply({ secret: 'for-d1' }), false);
  assert.equal(received[1].reply({ secret: 'for-d1' }), false);
  platform.eventLoop.flush();
  assert.deepEqual(newDocumentInbox, []);

  // The new document gets its own capability when it messages the host.
  platform.postMessage(sender, host, { n: 3 }, HOST);
  platform.eventLoop.flush();
  assert.equal(received.length, 3);
  assert.equal(received[2].reply({ ok: true }), true);
  platform.eventLoop.flush();
  assert.deepEqual(newDocumentInbox, [{ ok: true }]);
});

test('window object reuse does not inherit capabilities across generations', () => {
  const { platform, host, sender, received } = setup();

  platform.postMessage(sender, host, 'from-d1', HOST);
  platform.eventLoop.flush();
  assert.equal(received.length, 1);
  const d1Document = received[0].meta.document;

  sender.navigate(`${SENDER}/next`);
  platform.postMessage(sender, host, 'from-d2', HOST);
  platform.eventLoop.flush();
  assert.equal(received.length, 2);

  // Same reused window object backs both messages.
  const d2Inbox: Array<unknown> = [];
  sender.addMessageListener((event) => d2Inbox.push(event.data));

  // The capability issued to D1 is dead even though its window object is the
  // very object now hosting D2.
  assert.notEqual(received[0].meta.document, received[1].meta.document);
  assert.equal(received[0].meta.document, d1Document);
  assert.equal(received[0].reply('d1-secret'), false);
  assert.equal(received[1].reply('d2-reply'), true);
  platform.eventLoop.flush();
  assert.deepEqual(d2Inbox, ['d2-reply']);
});

test('a reply queued before navigation is re-checked at delivery time', () => {
  const { platform, host, sender, received } = setup();

  platform.postMessage(sender, host, 'ping', HOST);
  platform.eventLoop.flush();

  // Reply is accepted (document still current) and queued as a task...
  assert.equal(received[0].reply('pong'), true);
  // ...but the window navigates before the event loop delivers it.
  const leaked: Array<unknown> = [];
  sender.navigate(`${SENDER}/taken-over`);
  sender.addMessageListener((event) => leaked.push(event.data));

  platform.eventLoop.flush();
  assert.deepEqual(leaked, []);
});

test('messages from disallowed origins never reach the handler', () => {
  const { platform, host, received } = setup();
  const evil = platform.createWindow('evil', 'https://evil.example/');

  platform.postMessage(evil, host, 'spam', HOST);
  platform.eventLoop.flush();
  assert.equal(received.length, 0);
});

test('forged message events are ignored', () => {
  const { platform, host, sender, received } = setup();

  // Page code constructs a lookalike event and dispatches it directly.
  const forgedMeta = Object.freeze({ origin: SENDER, generation: sender.currentGeneration, document: sender.document });
  host.dispatchMessage(new PlatformMessageEvent('forged', sender, forgedMeta));
  assert.equal(received.length, 0);
});

test('trusted metadata is immutable and pinned to the send-time document', () => {
  const { platform, host, sender, received } = setup();

  const generationAtSend = sender.currentGeneration;
  platform.postMessage(sender, host, 'hello', HOST);
  platform.eventLoop.flush();
  assert.equal(received.length, 1);

  const meta = received[0].meta;
  assert.equal(meta.origin, SENDER);
  assert.equal(meta.generation, generationAtSend);
  assert.throws(() => {
    Object.assign(meta, { origin: 'https://evil.example' });
  }, TypeError);

  sender.navigate(`${SENDER}/elsewhere`);
  assert.equal(meta.origin, SENDER);
  assert.equal(meta.generation, generationAtSend);
  assert.notEqual(sender.currentGeneration, meta.generation);
});

test('control: a naive receiver that only pins origin does leak (harness sanity)', () => {
  // Proves the harness can actually observe the leak the secure receiver
  // prevents: pinning only (source window, origin) is not enough, because a
  // same-origin navigation keeps the origin check satisfied.
  const platform = new Platform();
  const host = platform.createWindow('host', `${HOST}/app`);
  const sender = platform.createWindow('sender', `${SENDER}/app`);

  let saved = null;
  host.addMessageListener((event) => {
    if (event.origin !== SENDER) return;
    saved = { source: event.source, origin: event.origin };
  });

  platform.postMessage(sender, host, 'hello', HOST);
  platform.eventLoop.flush();
  assert.ok(saved !== null);

  const leaked: Array<unknown> = [];
  sender.navigate(`${SENDER}/taken-over`);
  sender.addMessageListener((event) => leaked.push(event.data));

  platform.postMessage(host, saved.source, { secret: 'session-token' }, saved.origin);
  platform.eventLoop.flush();
  assert.deepEqual(leaked, [{ secret: 'session-token' }]);
});
