import { describe, test } from 'node:test';
import assert from 'node:assert/strict';

import {
  CardDavCollectionIsolation,
  HrefValidationError,
  InMemoryAddressbookStore,
  InvalidSyncTokenError,
  SyncTokenCodec,
  VALID_SYNC_TOKEN_FAILURE,
  isDirectMember,
  normalizeCollectionPath,
  normalizeHref,
} from '../src/index.ts';
import type {
  AddressbookCollection,
  AddressbookMember,
  AddressbookStore,
  MultigetItem,
} from '../src/index.ts';

const SECRET = 'carddav-test-secret-with-at-least-32-bytes';
const ALICE_ID = 'alice-default';
const BOB_ID = 'bob-default';
const ALICE_COLLECTION = '/addressbooks/alice/default';
const BOB_COLLECTION = '/addressbooks/bob/default';

interface Fixture {
  store: InMemoryAddressbookStore;
  tokens: SyncTokenCodec;
  isolation: CardDavCollectionIsolation;
  aliceCollection: AddressbookCollection;
  bobCollection: AddressbookCollection;
  aliceContact: AddressbookMember;
  bobContact: AddressbookMember;
}

function makeFixture(): Fixture {
  const store = new InMemoryAddressbookStore();
  const alice = store.addCollection({
    id: ALICE_ID,
    path: ALICE_COLLECTION,
    ownerPrincipalId: 'alice',
  });
  const bob = store.addCollection({
    id: BOB_ID,
    path: BOB_COLLECTION,
    ownerPrincipalId: 'bob',
  });

  const aliceContact = store.putMember(ALICE_ID, {
    href: `${ALICE_COLLECTION}/alice-1.vcf`,
    data: 'BEGIN:VCARD\nFN:Alice\nEND:VCARD',
  });
  const bobContact = store.putMember(BOB_ID, {
    href: `${BOB_COLLECTION}/bob-1.vcf`,
    data: 'BEGIN:VCARD\nFN:Bob\nEND:VCARD',
  });

  const tokens = new SyncTokenCodec(SECRET);
  const isolation = new CardDavCollectionIsolation({ store, tokens });
  const aliceCollection = store.getCollectionById(alice.id);
  const bobCollection = store.getCollectionById(bob.id);
  assert.ok(aliceCollection);
  assert.ok(bobCollection);

  return {
    store,
    tokens,
    isolation,
    aliceCollection,
    bobCollection,
    aliceContact,
    bobContact,
  };
}

function issueToken(
  tokens: SyncTokenCodec,
  collection: AddressbookCollection,
  principalId: string,
  revision: number,
): string {
  return tokens.issue({
    collectionId: collection.id,
    collectionPath: collection.path,
    principalId,
    revision,
  });
}

function assertUniformNotFound(item: MultigetItem): void {
  assert.equal(item.status, 404);
  assert.deepEqual(Object.keys(item).sort(), ['href', 'status']);
}

describe('href normalization', () => {
  test('decodes percent escapes once and rejects decoded dot segments', () => {
    assert.throws(
      () => normalizeHref('/addressbooks/alice/default/%2e%2e/bob/x.vcf'),
      (error: unknown) =>
        error instanceof HrefValidationError &&
        error.reason === 'dot-segment',
    );

    const doubleEncoded = normalizeHref(
      '/addressbooks/alice/default/%252e%252e',
    );
    assert.deepEqual(doubleEncoded.segments.slice(-1), ['%2e%2e']);
    assert.notEqual(doubleEncoded.segments.at(-1), '..');
  });

  test('encoded slashes and backslashes never become path separators', () => {
    for (const href of [
      '/addressbooks/alice/default/one%2ftwo.vcf',
      '/addressbooks/alice/default/one%5ctwo.vcf',
      '/addressbooks/alice/default%2f..%2fbob%2fdefault%2fx.vcf',
    ]) {
      assert.throws(
        () => normalizeHref(href),
        (error: unknown) =>
          error instanceof HrefValidationError &&
          error.reason === 'encoded-separator',
      );
    }
  });

  test('requires an absolute path and rejects traversal, nested paths and prefix traps', () => {
    const collection = normalizeCollectionPath(`${ALICE_COLLECTION}/`);
    assert.equal(collection.path, ALICE_COLLECTION);

    assert.equal(
      isDirectMember(
        collection,
        normalizeHref(`${ALICE_COLLECTION}/alice-1.vcf`),
      ),
      true,
    );

    for (const href of [
      `${ALICE_COLLECTION}/nested/alice-1.vcf`,
      '/addressbooks/alice/default-other/alice-1.vcf',
    ]) {
      assert.equal(isDirectMember(collection, normalizeHref(href)), false);
    }

    for (const href of [
      `${ALICE_COLLECTION}/../bob/default/bob-1.vcf`,
      `${ALICE_COLLECTION}/alice-1.vcf/`,
      'addressbooks/alice/default/alice-1.vcf',
    ]) {
      assert.throws(() => normalizeHref(href), HrefValidationError);
    }
  });

  test('rejects malformed escapes, controls, queries and fragments', () => {
    for (const href of [
      `${ALICE_COLLECTION}/%zz.vcf`,
      `${ALICE_COLLECTION}/%00.vcf`,
      `${ALICE_COLLECTION}/%0a.vcf`,
      `${ALICE_COLLECTION}/alice-1.vcf?download=1`,
      `${ALICE_COLLECTION}/alice-1.vcf#fragment`,
    ]) {
      assert.throws(() => normalizeHref(href), HrefValidationError);
    }
  });

  test('absolute hrefs are bound to the request authority', () => {
    const normalized = normalizeHref(
      `https://dav.example.com${ALICE_COLLECTION}/alice-1.vcf`,
      { requestAuthority: 'dav.example.com' },
    );
    assert.equal(normalized.path, `${ALICE_COLLECTION}/alice-1.vcf`);

    assert.throws(
      () =>
        normalizeHref(`https://evil.example.com${ALICE_COLLECTION}/alice-1.vcf`, {
          requestAuthority: 'dav.example.com',
        }),
      (error: unknown) =>
        error instanceof HrefValidationError &&
        error.reason === 'foreign-authority',
    );
  });
});

describe('addressbook-multiget isolation', () => {
  test('returns own direct members and data', () => {
    const fixture = makeFixture();
    const report = fixture.isolation.multiget({
      principalId: 'alice',
      collectionPath: ALICE_COLLECTION,
      hrefs: [`${ALICE_COLLECTION}/alice-1.vcf`],
    });

    assert.equal(report.status, 207);
    assert.equal(report.items.length, 1);
    const item = report.items[0];
    assert.equal(item.status, 200);
    assert.equal(item.href, fixture.aliceContact.href);
    assert.equal(item.member.data, fixture.aliceContact.data);
  });

  test('cross-collection, nested, prefix and encoded traversal hrefs are 404', () => {
    const fixture = makeFixture();
    const hrefs = [
      `${BOB_COLLECTION}/bob-1.vcf`,
      `${ALICE_COLLECTION}/nested/alice-1.vcf`,
      '/addressbooks/alice/default-other/alice-1.vcf',
      `${ALICE_COLLECTION}/%2e%2e/bob/default/bob-1.vcf`,
      `${ALICE_COLLECTION}/alice%2f..%2fbob%2fdefault%2fbob-1.vcf`,
      `${ALICE_COLLECTION}/%2f..%2fbob%2fdefault%2fbob-1.vcf`,
      `${ALICE_COLLECTION}/..%2fbob%2fdefault%2fbob-1.vcf`,
      `${ALICE_COLLECTION}/alice-1.vcf/`,
      'addressbooks/alice/default/alice-1.vcf',
    ];

    const report = fixture.isolation.multiget({
      principalId: 'alice',
      collectionPath: ALICE_COLLECTION,
      hrefs,
    });

    assert.equal(report.status, 207);
    assert.equal(report.items.length, hrefs.length);
    for (const item of report.items) {
      assertUniformNotFound(item);
    }
  });

  test('existing, deleted, missing and out-of-scope members share one 404 shape', () => {
    const fixture = makeFixture();
    const deletedHref = `${ALICE_COLLECTION}/deleted.vcf`;
    fixture.store.putMember(ALICE_ID, {
      href: deletedHref,
      data: 'BEGIN:VCARD\nFN:Deleted\nEND:VCARD',
    });
    assert.equal(fixture.store.deleteMember(ALICE_ID, deletedHref), true);

    const report = fixture.isolation.multiget({
      principalId: 'alice',
      collectionPath: ALICE_COLLECTION,
      hrefs: [
        deletedHref,
        `${ALICE_COLLECTION}/never-existed.vcf`,
        `${BOB_COLLECTION}/bob-1.vcf`,
      ],
    });

    assert.equal(report.status, 207);
    for (const item of report.items) {
      assertUniformNotFound(item);
    }
  });

  test('a double-encoded name is literal, while single-encoded dots are traversal', () => {
    const fixture = makeFixture();
    const literalHref = `${ALICE_COLLECTION}/%252e%252e`;
    fixture.store.putMember(ALICE_ID, {
      href: literalHref,
      data: 'BEGIN:VCARD\nFN:Literal Percent\nEND:VCARD',
    });

    const report = fixture.isolation.multiget({
      principalId: 'alice',
      collectionPath: ALICE_COLLECTION,
      hrefs: [literalHref, `${ALICE_COLLECTION}/%2e%2e`],
    });

    assert.equal(report.items[0].status, 200);
    assert.equal(report.items[0].href, literalHref);
    assertUniformNotFound(report.items[1]);
  });

  test('per-member read denial is indistinguishable from a missing href', () => {
    const fixture = makeFixture();

    const shared = fixture.store.addCollection({
      id: 'shared-read',
      path: '/addressbooks/shared/team',
      ownerPrincipalId: 'alice',
      readableBy: ['alice', 'bob'],
    });
    const privateContact = fixture.store.putMember(shared.id, {
      href: `${shared.path}/private.vcf`,
      data: 'BEGIN:VCARD\nFN:Private\nEND:VCARD',
      readableBy: ['alice'],
    });
    fixture.store.putMember(shared.id, {
      href: `${shared.path}/team.vcf`,
      data: 'BEGIN:VCARD\nFN:Team\nEND:VCARD',
    });

    const report = fixture.isolation.multiget({
      principalId: 'bob',
      collectionPath: shared.path,
      hrefs: [privateContact.href, `${shared.path}/missing.vcf`],
    });

    assert.equal(report.status, 207);
    assertUniformNotFound(report.items[0]);
    assertUniformNotFound(report.items[1]);
  });

  test('an unreadable REPORT target is a top-level 404', () => {
    const fixture = makeFixture();
    const report = fixture.isolation.multiget({
      principalId: 'bob',
      collectionPath: ALICE_COLLECTION,
      hrefs: [`${ALICE_COLLECTION}/alice-1.vcf`],
    });

    assert.deepEqual(report, {
      status: 404,
      error: 'collection-unavailable',
    });
  });

  test('absolute hrefs require the request authority and direct membership', () => {
    const fixture = makeFixture();
    const report = fixture.isolation.multiget({
      principalId: 'alice',
      collectionPath: ALICE_COLLECTION,
      requestAuthority: 'dav.example.com',
      hrefs: [
        `https://dav.example.com${ALICE_COLLECTION}/alice-1.vcf`,
        `https://evil.example.com${ALICE_COLLECTION}/alice-1.vcf`,
      ],
    });

    assert.equal(report.items[0].status, 200);
    assertUniformNotFound(report.items[1]);
  });
});

describe('sync-collection token isolation', () => {
  test('initial sync returns readable members and a bound token', () => {
    const fixture = makeFixture();
    const report = fixture.isolation.syncCollection({
      principalId: 'alice',
      collectionPath: ALICE_COLLECTION,
    });

    assert.equal(report.status, 207);
    assert.equal(report.changes.length, 1);
    assert.equal(report.changes[0].status, 200);
    assert.equal(report.changes[0].href, fixture.aliceContact.href);

    const claims = fixture.tokens.verify(report.syncToken, {
      collectionId: ALICE_ID,
      collectionPath: ALICE_COLLECTION,
      principalId: 'alice',
    });
    assert.equal(claims.revision, fixture.aliceCollection.revision);
  });

  test('a token from another collection cannot expose that collection deletions', () => {
    const fixture = makeFixture();

    const deletedHref = `${ALICE_COLLECTION}/deleted.vcf`;
    fixture.store.putMember(ALICE_ID, {
      href: deletedHref,
      data: 'BEGIN:VCARD\nFN:Deleted\nEND:VCARD',
    });
    const beforeDelete = fixture.store.getCollectionById(ALICE_ID);
    assert.ok(beforeDelete);
    const aliceBeforeDelete = issueToken(
      fixture.tokens,
      beforeDelete,
      'alice',
      beforeDelete.revision,
    );

    assert.equal(fixture.store.deleteMember(ALICE_ID, deletedHref), true);

    const ownSync = fixture.isolation.syncCollection({
      principalId: 'alice',
      collectionPath: ALICE_COLLECTION,
      syncToken: aliceBeforeDelete,
    });
    assert.equal(ownSync.status, 207);
    const deletion = ownSync.changes.find(
      (change) => change.href === deletedHref,
    );
    assert.ok(deletion);
    assert.equal(deletion.status, 404);

    const bobToken = issueToken(
      fixture.tokens,
      fixture.bobCollection,
      'bob',
      fixture.bobCollection.revision,
    );
    const swapped = fixture.isolation.syncCollection({
      principalId: 'alice',
      collectionPath: ALICE_COLLECTION,
      syncToken: bobToken,
    });
    assert.deepEqual(swapped, VALID_SYNC_TOKEN_FAILURE);
  });

  test('token principal binding survives shared collection read access', () => {
    const fixture = makeFixture();
    const shared = fixture.store.addCollection({
      id: 'shared-sync',
      path: '/addressbooks/shared/sync',
      ownerPrincipalId: 'alice',
      readableBy: ['alice', 'bob'],
    });
    fixture.store.putMember(shared.id, {
      href: `${shared.path}/one.vcf`,
      data: 'BEGIN:VCARD\nFN:One\nEND:VCARD',
    });

    const aliceToken = issueToken(fixture.tokens, shared, 'alice', shared.revision);
    const report = fixture.isolation.syncCollection({
      principalId: 'bob',
      collectionPath: shared.path,
      syncToken: aliceToken,
    });

    assert.deepEqual(report, VALID_SYNC_TOKEN_FAILURE);
  });

  test('tokens from another collection of the same principal are rejected', () => {
    const fixture = makeFixture();
    const work = fixture.store.addCollection({
      id: 'alice-work',
      path: '/addressbooks/alice/work',
      ownerPrincipalId: 'alice',
    });
    fixture.store.putMember(work.id, {
      href: `${work.path}/work-1.vcf`,
      data: 'BEGIN:VCARD\nFN:Work\nEND:VCARD',
    });

    const workSync = fixture.isolation.syncCollection({
      principalId: 'alice',
      collectionPath: work.path,
    });
    assert.equal(workSync.status, 207);

    const swapped = fixture.isolation.syncCollection({
      principalId: 'alice',
      collectionPath: ALICE_COLLECTION,
      syncToken: workSync.syncToken,
    });
    assert.deepEqual(swapped, VALID_SYNC_TOKEN_FAILURE);
  });

  test('tampered, future, expired and malformed tokens return valid-sync-token', () => {
    const fixture = makeFixture();
    const goodToken = issueToken(
      fixture.tokens,
      fixture.aliceCollection,
      'alice',
      fixture.aliceCollection.revision,
    );

    const futureToken = issueToken(
      fixture.tokens,
      fixture.aliceCollection,
      'alice',
      fixture.aliceCollection.revision + 100,
    );

    const parts = goodToken.split('.');
    const mac = parts.at(-1);
    assert.ok(mac);
    parts[parts.length - 1] =
      `${mac[0] === 'A' ? 'B' : 'A'}${mac.slice(1)}`;
    const tamperedToken = parts.join('.');

    for (const token of [
      tamperedToken,
      futureToken,
      'not-a-token',
      `${goodToken}tampered`,
    ]) {
      const report = fixture.isolation.syncCollection({
        principalId: 'alice',
        collectionPath: ALICE_COLLECTION,
        syncToken: token,
      });
      assert.deepEqual(report, VALID_SYNC_TOKEN_FAILURE);
    }

    fixture.store.pruneHistory(ALICE_ID, fixture.store.getCollectionById(ALICE_ID)!.revision);
    const expiredToken = issueToken(
      fixture.tokens,
      fixture.aliceCollection,
      'alice',
      0,
    );
    const expired = fixture.isolation.syncCollection({
      principalId: 'alice',
      collectionPath: ALICE_COLLECTION,
      syncToken: expiredToken,
    });
    assert.deepEqual(expired, VALID_SYNC_TOKEN_FAILURE);
  });

  test('a token is bound to collection id, path and revision range', () => {
    const fixture = makeFixture();
    const token = issueToken(
      fixture.tokens,
      fixture.aliceCollection,
      'alice',
      fixture.aliceCollection.revision,
    );

    assert.throws(
      () =>
        fixture.tokens.verify(token, {
          collectionId: BOB_ID,
          collectionPath: ALICE_COLLECTION,
          principalId: 'alice',
        }),
      (error: unknown) =>
        error instanceof InvalidSyncTokenError &&
        error.reason === 'collection-mismatch',
    );

    assert.throws(
      () =>
        fixture.tokens.verify(token, {
          collectionId: ALICE_ID,
          collectionPath: `${ALICE_COLLECTION}/`,
          principalId: 'alice',
        }),
      (error: unknown) =>
        error instanceof InvalidSyncTokenError &&
        error.reason === 'collection-mismatch',
    );
  });

  test('a token only returns changes after its revision, without repeating deletions', () => {
    const fixture = makeFixture();
    const firstToken = issueToken(
      fixture.tokens,
      fixture.aliceCollection,
      'alice',
      fixture.aliceCollection.revision,
    );

    fixture.store.putMember(ALICE_ID, {
      href: `${ALICE_COLLECTION}/alice-2.vcf`,
      data: 'BEGIN:VCARD\nFN:Alice Two\nEND:VCARD',
    });
    const second = fixture.isolation.syncCollection({
      principalId: 'alice',
      collectionPath: ALICE_COLLECTION,
      syncToken: firstToken,
    });
    assert.equal(second.status, 207);
    assert.equal(second.changes.length, 1);
    assert.equal(second.changes[0].status, 200);
    assert.equal(second.changes[0].href, `${ALICE_COLLECTION}/alice-2.vcf`);

    const third = fixture.isolation.syncCollection({
      principalId: 'alice',
      collectionPath: ALICE_COLLECTION,
      syncToken: second.syncToken,
    });
    assert.equal(third.status, 207);
    assert.deepEqual(third.changes, []);
  });

  test('a faulty store cannot inject another collection tombstone into sync output', () => {
    const fixture = makeFixture();
    const token = issueToken(
      fixture.tokens,
      fixture.aliceCollection,
      'alice',
      0,
    );

    const leakyStore: AddressbookStore = {
      getCollectionByPath: (path) => fixture.store.getCollectionByPath(path),
      getCollectionById: (id) => fixture.store.getCollectionById(id),
      getMember: (collectionId, href) =>
        fixture.store.getMember(collectionId, href),
      listMembers: (collectionId) => fixture.store.listMembers(collectionId),
      changesSince: (collectionId, revision) => [
        ...fixture.store.changesSince(collectionId, revision),
        {
          collectionId,
          href: `${BOB_COLLECTION}/bob-1.vcf`,
          revision: fixture.store.getCollectionById(ALICE_ID)!.revision,
          kind: 'deleted',
        },
      ],
    };

    const guarded = new CardDavCollectionIsolation({
      store: leakyStore,
      tokens: fixture.tokens,
    });
    const report = guarded.syncCollection({
      principalId: 'alice',
      collectionPath: ALICE_COLLECTION,
      syncToken: token,
    });

    assert.equal(report.status, 207);
    assert.deepEqual(
      report.changes.map((change) => change.href),
      [fixture.aliceContact.href],
    );
  });

  test('deletions are returned once to the bound principal and then consumed', () => {
    const fixture = makeFixture();
    const beforeDeleteCollection = fixture.store.getCollectionById(ALICE_ID);
    assert.ok(beforeDeleteCollection);
    const beforeDelete = issueToken(
      fixture.tokens,
      beforeDeleteCollection,
      'alice',
      beforeDeleteCollection.revision,
    );

    fixture.store.deleteMember(ALICE_ID, `${ALICE_COLLECTION}/alice-1.vcf`);

    const afterDelete = fixture.isolation.syncCollection({
      principalId: 'alice',
      collectionPath: ALICE_COLLECTION,
      syncToken: beforeDelete,
    });
    assert.equal(afterDelete.status, 207);
    assert.equal(afterDelete.changes.length, 1);
    assert.equal(afterDelete.changes[0].status, 404);
    assert.equal(afterDelete.changes[0].deleted, true);

    const next = fixture.isolation.syncCollection({
      principalId: 'alice',
      collectionPath: ALICE_COLLECTION,
      syncToken: afterDelete.syncToken,
    });
    assert.equal(next.status, 207);
    assert.deepEqual(next.changes, []);
  });
});
