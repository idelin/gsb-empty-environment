import assert from 'node:assert/strict';
import { test } from 'node:test';

import {
  HSTSPolicyLibrary,
  PartitionRegistry,
  createSignedScopeSummary,
  dnsRelation,
  exportAffectedSubtrees,
  isSubdomainOf,
  normalizeDnsName,
  restoreCheckpoint,
  verifyAndRestoreCheckpoint,
  verifySignedScopeSummary,
  type EventInput,
  type HSTSLibraryOptions,
} from '../src/index.ts';

const priority = ['enterprise', 'https', 'user'] as const;
const options: HSTSLibraryOptions = { sourcePriority: priority as unknown as string[] };

test('normalizes case, trailing root dot, and IDN Punycode labels', () => {
  assert.equal(normalizeDnsName('WWW.Example.COM.').fqdn, 'www.example.com');
  assert.equal(normalizeDnsName('BÜCHER.example').fqdn, 'xn--bcher-kva.example');
  assert.equal(normalizeDnsName('sub.XN--BCHER-KVA.example').fqdn, 'sub.xn--bcher-kva.example');
  assert.deepEqual(normalizeDnsName('a.example.test').labels, ['a', 'example', 'test']);
  assert.equal(dnsRelation('deep.child.example.test', 'example.test'), 'subdomain');
  assert.equal(isSubdomainOf('example.test', 'child.example.test'), false);
});

test('deleting a child version leaves same-tree parent suffix inheritance intact', () => {
  const library = new HSTSPolicyLibrary(options);
  library.add({
    eventId: 1,
    host: 'example.test',
    partition: 'p',
    source: 'enterprise',
    issuer: 'enterprise-root',
    includeSubDomains: true,
    expiresAt: 1_000,
  });
  library.add({
    eventId: 2,
    host: 'child.example.test',
    partition: 'p',
    source: 'https',
    issuer: 'https-ca',
    includeSubDomains: false,
    expiresAt: 1_000,
  });

  const deleted = library.delete('child.example.test', 2, {
    eventId: 3,
    partition: 'p',
    source: 'https',
  });
  assert.equal(deleted.applied, true);
  assert.equal(library.deletionStatus(library.records.get('number:1')!).status, 'live');
  assert.equal(library.deletionStatus(library.records.get('number:2')!).status, 'deleted');

  const child = library.evaluate('child.example.test', { at: 100, partition: 'p' });
  assert.equal(child.shouldUpgrade, true);
  assert.equal(child.winner?.eventId, 1);
  assert.ok(child.reasonCodes.includes('suffix-inheritance'));

  const longName = `a.b.c.d.e.f.deep.child.example.test`;
  const deep = library.evaluate(longName, { at: 100, partition: 'p' });
  assert.equal(deep.shouldUpgrade, true);
  assert.equal(deep.winner?.eventId, 1);
  assert.equal(deep.basis.find((item) => item.eventId === 1)?.match, 'subdomain');
});

test('a delete revokes only its explicitly observed version and old late adds stay tombstoned', () => {
  const library = new HSTSPolicyLibrary(options);
  library.add({ eventId: 10, host: 'versioned.example.test', partition: 'p', source: 'https', expiresAt: 1_000 });
  library.add({ eventId: 11, host: 'versioned.example.test', partition: 'p', source: 'https', expiresAt: 1_000 });
  library.delete({ eventId: 12, observedEventId: 10, partition: 'p', source: 'https' });

  assert.equal(library.deletionStatus(library.records.get('number:10')!).status, 'deleted');
  assert.equal(library.deletionStatus(library.records.get('number:11')!).status, 'live');

  const lateFirstDelete = new HSTSPolicyLibrary(options);
  const confirmedDelete = lateFirstDelete.delete({
    eventId: 5,
    observedEventId: 3,
    host: 'late.example.test',
    partition: 'p',
    source: 'https',
    confirmed: true,
  });
  assert.equal(confirmedDelete.pending, true);

  const lateAdd = lateFirstDelete.add({
    eventId: 3,
    host: 'late.example.test',
    partition: 'p',
    source: 'https',
    expiresAt: 1_000,
  });
  assert.equal(lateAdd.hiddenByTombstone, true);
  assert.equal(lateFirstDelete.isConfirmedDeleted(3), true);
  assert.equal(lateFirstDelete.shouldUpgrade('late.example.test', { at: 10, partition: 'p' }), false);

  lateFirstDelete.add({
    eventId: 6,
    host: 'late.example.test',
    partition: 'p',
    source: 'https',
    expiresAt: 1_000,
  });
  assert.equal(lateFirstDelete.shouldUpgrade('late.example.test', { at: 10, partition: 'p' }), true);
});

test('host source priority is explicit, while locked enterprise rules reject user weakening', () => {
  const lockedFirst = new HSTSPolicyLibrary({ sourcePriority: ['user', 'https', 'enterprise'] });
  lockedFirst.add({
    eventId: 1,
    host: 'locked.example.test',
    partition: 'p',
    source: 'enterprise',
    issuer: 'enterprise-policy-signer',
    includeSubDomains: true,
    expiresAt: 1_000,
    locked: true,
  });
  lockedFirst.add({
    eventId: 2,
    host: 'child.locked.example.test',
    partition: 'p',
    source: 'user',
    effect: 'allow',
    expiresAt: 1_000,
  });

  const decision = lockedFirst.evaluate('child.locked.example.test', { at: 10, partition: 'p' });
  assert.equal(decision.shouldUpgrade, true);
  assert.equal(decision.winner?.eventId, 1);
  assert.ok(decision.reasonCodes.includes('mandatory-enterprise-rule'));
  assert.equal(decision.blocked?.[0]?.eventId, 2);

  const blockedDelete = lockedFirst.delete({
    eventId: 3,
    observedEventId: 1,
    partition: 'p',
    source: 'user',
  });
  assert.equal(blockedDelete.applied, false);
  assert.equal(blockedDelete.blocked, true);
  assert.equal(
    lockedFirst.deletionStatus(lockedFirst.records.get('number:1')!).status,
    'blocked',
  );
  assert.equal(
    lockedFirst.shouldUpgrade('anything.locked.example.test', { at: 10, partition: 'p' }),
    true,
  );

  const enterpriseDelete = lockedFirst.delete({
    eventId: 4,
    observedEventId: 1,
    partition: 'p',
    source: 'enterprise',
  });
  assert.equal(enterpriseDelete.applied, true);
  assert.equal(
    lockedFirst.shouldUpgrade('locked.example.test', { at: 10, partition: 'p' }),
    false,
  );

  const userPriority = new HSTSPolicyLibrary({ sourcePriority: ['user', 'enterprise'] });
  userPriority.add({
    eventId: 1,
    host: 'priority.example.test',
    partition: 'p',
    source: 'enterprise',
    includeSubDomains: true,
    expiresAt: 1_000,
  });
  userPriority.add({
    eventId: 2,
    host: 'child.priority.example.test',
    partition: 'p',
    source: 'user',
    effect: 'allow',
    expiresAt: 1_000,
  });
  assert.equal(
    userPriority.shouldUpgrade('child.priority.example.test', { at: 10, partition: 'p' }),
    false,
  );
});

test('closed time-set unions distinguish always, never, and insufficient evidence across expiry', () => {
  const library = new HSTSPolicyLibrary(options);
  library.add({
    eventId: 1,
    host: 'time.example.test',
    partition: 'p',
    source: 'enterprise',
    includeSubDomains: true,
    expiresAt: 100,
  });

  const query = (time: Parameters<HSTSPolicyLibrary['evaluateTemporal']>[1]) =>
    library.evaluateTemporal('child.time.example.test', time, { partition: 'p' }).verdict;

  assert.equal(query([[80, 99]]), 'always-upgrade');
  assert.equal(query([[100, 110]]), 'never-upgrade');
  assert.equal(query([[90, 110]]), 'evidence-insufficient');
  assert.equal(query([[80, 95], [105, 120]]), 'evidence-insufficient');
  assert.equal(query([[101, 120], [130, 140]]), 'never-upgrade');

  const inclusive = new HSTSPolicyLibrary({ ...options, expiryBoundary: 'inclusive' });
  inclusive.add({ eventId: 1, host: 'time.example.test', partition: 'p', expiresAt: 100 });
  assert.equal(
    inclusive.evaluateTemporal('time.example.test', [[100, 100]], { partition: 'p' }).verdict,
    'always-upgrade',
  );
});

test('parent and child expiry order does not destroy the still-valid policy', () => {
  const childExpiresFirst = new HSTSPolicyLibrary(options);
  childExpiresFirst.add({
    eventId: 1,
    host: 'example.test',
    partition: 'p',
    includeSubDomains: true,
    expiresAt: 300,
  });
  childExpiresFirst.add({
    eventId: 2,
    host: 'child.example.test',
    partition: 'p',
    includeSubDomains: true,
    expiresAt: 100,
  });
  const first = childExpiresFirst.evaluateTemporal('deep.child.example.test', [[150, 250]], {
    partition: 'p',
  });
  assert.equal(first.verdict, 'always-upgrade');
  assert.equal(first.probes[0] && childExpiresFirst.evaluate('deep.child.example.test', { at: 200, partition: 'p' }).winner?.eventId, 1);

  const parentExpiresFirst = new HSTSPolicyLibrary(options);
  parentExpiresFirst.add({
    eventId: 1,
    host: 'example.test',
    partition: 'p',
    includeSubDomains: true,
    expiresAt: 100,
  });
  parentExpiresFirst.add({
    eventId: 2,
    host: 'child.example.test',
    partition: 'p',
    includeSubDomains: true,
    expiresAt: 300,
  });
  const second = parentExpiresFirst.evaluateTemporal('deep.child.example.test', [[150, 250]], {
    partition: 'p',
  });
  assert.equal(second.verdict, 'always-upgrade');
  assert.equal(
    parentExpiresFirst.evaluate('deep.child.example.test', { at: 200, partition: 'p' }).winner?.eventId,
    2,
  );

  const parentOnly = new HSTSPolicyLibrary(options);
  parentOnly.add({ eventId: 1, host: 'example.test', partition: 'p', includeSubDomains: true, expiresAt: 100 });
  assert.equal(
    parentOnly.evaluateTemporal('child.example.test', [[90, 110]], { partition: 'p' }).verdict,
    'evidence-insufficient',
  );
});

test('partition generations isolate same-name partitions after device reinstall', () => {
  const registry = new PartitionRegistry();
  const beforeReinstall = registry.current('cache');
  const library = new HSTSPolicyLibrary(options);
  library.add({
    eventId: 1,
    host: 'partition.example.test',
    partition: beforeReinstall,
    source: 'user',
    expiresAt: 1_000,
  });
  assert.equal(library.shouldUpgrade('partition.example.test', { at: 10, partition: beforeReinstall }), true);

  const afterReinstall = registry.reinstall('cache');
  assert.equal(afterReinstall.generation, 1);
  assert.equal(library.shouldUpgrade('partition.example.test', { at: 10, partition: afterReinstall }), false);

  const checkpoint = library.buildCheckpoint({ partition: beforeReinstall, host: 'partition.example.test' });
  assert.equal(checkpoint.partitionGeneration, '0');
  assert.throws(
    () => restoreCheckpoint(checkpoint, { expectedPartition: afterReinstall }),
    /partition generation/,
  );
  assert.throws(
    () => restoreCheckpoint(checkpoint, { partitionRegistry: registry }),
    /partition generation/,
  );

  const coordinated = new HSTSPolicyLibrary(options);
  coordinated.add({
    eventId: 1,
    host: 'coordinated.example.test',
    partition: { id: 'network', generation: 0, topLevelSite: 'a.test' },
    expiresAt: 1000,
  });
  assert.equal(
    coordinated.shouldUpgrade('coordinated.example.test', {
      at: 10,
      partition: { id: 'network', generation: 0, topLevelSite: 'a.test' },
    }),
    true,
  );
  assert.equal(
    coordinated.shouldUpgrade('coordinated.example.test', {
      at: 10,
      partition: { id: 'network', generation: 0, topLevelSite: 'b.test' },
    }),
    false,
  );
});

test('signed scope digests are prefix-complete and exclude other partitions', async () => {
  const events: EventInput[] = [
    { eventId: 1, host: 'example.test', partition: { id: 'p1', generation: 3 }, source: 'enterprise', expiresAt: 1000 },
    { eventId: 2, host: 'inside.example.test', partition: { id: 'p1', generation: 3 }, source: 'https', expiresAt: 1000 },
    { eventId: 3, host: 'other.example.test', partition: { id: 'p2', generation: 3 }, source: 'user', expiresAt: 1000 },
  ];
  const secret = 'device-sync-secret';
  const summary = await createSignedScopeSummary({
    prefix: 'example.test',
    partition: { id: 'p1', generation: 3 },
    events,
    signer: 'offline-device-a',
    throughEventId: 2,
  }, secret);

  assert.equal((await verifySignedScopeSummary(summary, secret, {
    partition: { id: 'p1', generation: 3 },
    host: 'a.long.inside.example.test',
  })).valid, true);
  assert.equal((await verifySignedScopeSummary(summary, secret, {
    partition: { id: 'p1', generation: 4 },
  })).valid, false);
  assert.equal((await verifySignedScopeSummary({ ...summary, throughEventId: 999 }, secret)).valid, false);

  const library = HSTSPolicyLibrary.replay(events, options);
  library.addScopeSummary(summary);
  assert.equal(library.isScopeComplete('any.deep.example.test', { id: 'p1', generation: 3 }), true);
  assert.equal(library.isScopeComplete('any.deep.example.test', { id: 'p2', generation: 3 }), false);
  assert.equal(library.isScopeComplete('outside.test', { id: 'p1', generation: 3 }), false);
});

test('exports minimal affected subtrees with decision basis', () => {
  const library = new HSTSPolicyLibrary(options);
  library.add({ eventId: 1, host: 'example.test', partition: 'p', includeSubDomains: true, expiresAt: 1000 });
  library.add({ eventId: 2, host: 'a.example.test', partition: 'p', includeSubDomains: true, expiresAt: 1000 });
  library.add({ eventId: 3, host: 'b.example.test', partition: 'p', includeSubDomains: false, expiresAt: 1000 });
  library.add({ eventId: 4, host: 'other.test', partition: 'p', includeSubDomains: true, expiresAt: 1000 });

  const exported = library.exportAffectedSubtrees({ includeDecisions: true, at: 100, partition: 'p' });
  assert.deepEqual(exported.minimalRoots.map((item) => item.root), ['example.test', 'other.test']);
  assert.equal(exported.entries.length, 4);
  assert.ok(exported.subtrees[0]?.basis.length);
  assert.equal(exported.subtrees[0]?.decision?.shouldUpgrade, true);

  const separate = exportAffectedSubtrees([
    { eventId: 1, host: 'a.example.test', partition: 'p', includeSubDomains: true, expiresAt: 1000 },
    { eventId: 2, host: 'b.example.test', partition: 'p', includeSubDomains: true, expiresAt: 1000 },
  ]);
  assert.deepEqual(separate.minimalRoots.map((item) => item.root).sort(), ['a.example.test', 'b.example.test']);
});

test('checkpoint binds suffix rules and survives JSON recovery', async () => {
  const library = new HSTSPolicyLibrary(options);
  library.add({ eventId: 1, host: 'example.test', partition: 'p', includeSubDomains: true, expiresAt: 1000 });
  library.add({ eventId: 2, host: 'child.example.test', partition: 'p', expiresAt: 1000 });
  library.delete({ eventId: 3, observedEventId: 2, partition: 'p' });

  const checkpoint = await library.createCheckpoint({
    host: 'deep.child.example.test',
    partition: 'p',
    secret: 'checkpoint-secret',
  });
  assert.ok(checkpoint.signature);
  assert.ok(checkpoint.suffixRulesDigest);
  assert.equal(checkpoint.partitionGeneration, '0');

  const json = JSON.stringify(checkpoint);
  const restored = await verifyAndRestoreCheckpoint(json, 'checkpoint-secret');
  assert.equal(restored.shouldUpgrade('deep.child.example.test', { at: 10, partition: 'p' }), true);

  const badSignature = JSON.parse(json);
  badSignature.signature = `bad${badSignature.signature}`;
  await assert.rejects(() => verifyAndRestoreCheckpoint(badSignature, 'checkpoint-secret'), /signature/);

  const tampered = JSON.parse(json);
  tampered.snapshot.events.find((event: { eventId: number }) => event.eventId === 1).expiresAt = 1;
  assert.throws(() => restoreCheckpoint(tampered), /suffix rules/);
});

function smallHistory(): EventInput[] {
  return [
    { kind: 'add', eventId: 1, host: 'example.test', partition: 'p', source: 'enterprise', issuer: 'ent', includeSubDomains: true, expiresAt: 300 },
    { kind: 'add', eventId: 2, host: 'child.example.test', partition: 'p', source: 'https', issuer: 'ca-a', includeSubDomains: true, expiresAt: 200 },
    { kind: 'delete', eventId: 3, observedEventId: 2, host: 'child.example.test', partition: 'p', source: 'https' },
    { kind: 'add', eventId: 4, host: 'allow-name.test', partition: 'p', source: 'user', effect: 'allow', expiresAt: 400 },
    { kind: 'add', eventId: 5, host: 'dup-name.test', partition: 'p', source: 'https', issuer: 'ca-a', expiresAt: 500 },
    { kind: 'add', eventId: 6, host: 'dup-name.test', partition: 'p', source: 'https', issuer: 'ca-b', expiresAt: 500 },
    { kind: 'delete', eventId: 7, observedEventId: 6, host: 'dup-name.test', partition: 'p', source: 'https', issuer: 'ca-b' },
    { kind: 'add', eventId: 8, host: 'locked.example.test', partition: 'p', source: 'enterprise', includeSubDomains: true, locked: true, expiresAt: 500 },
    { kind: 'add', eventId: 9, host: 'user.locked.example.test', partition: 'p', source: 'user', effect: 'allow', expiresAt: 500 },
  ];
}

test('incremental prefixes and full monotonic replay agree over the small tree', () => {
  const history = smallHistory();
  const hosts = [
    'example.test',
    'child.example.test',
    'a.very.long.deep.child.example.test',
    'allow-name.test',
    'dup-name.test',
    'locked.example.test',
    'user.locked.example.test',
    'missing.example.test',
  ];
  const times = [50, 150, 250, 350, 450];
  const incremental = new HSTSPolicyLibrary(options);

  history.forEach((event, index) => {
    incremental.applyEvent(event);
    const replayed = HSTSPolicyLibrary.replay(history.slice(0, index + 1), options);
    for (const host of hosts) {
      for (const at of times) {
        const left = incremental.evaluate(host, { at, partition: 'p' });
        const right = replayed.evaluate(host, { at, partition: 'p' });
        assert.equal(left.shouldUpgrade, right.shouldUpgrade, `upgrade mismatch at event ${index + 1}, ${host}, ${at}`);
        assert.equal(left.winner?.eventId, right.winner?.eventId, `winner mismatch at event ${index + 1}, ${host}, ${at}`);
        assert.deepEqual(left.reasonCodes, right.reasonCodes);
      }
      for (const time of [[[150, 250]], [[250, 350]]] as const) {
        const left = incremental.evaluateTemporal(host, time[0], { partition: 'p' }).verdict;
        const right = replayed.evaluateTemporal(host, time[0], { partition: 'p' }).verdict;
        assert.equal(left, right, `temporal mismatch at event ${index + 1}, ${host}`);
      }
    }
  });

  const finalDecision = HSTSPolicyLibrary.replay(history, options);
  assert.equal(finalDecision.shouldUpgrade('deep.child.example.test', { at: 250, partition: 'p' }), true);
  assert.equal(finalDecision.evaluate('dup-name.test', { at: 50, partition: 'p' }).winner?.eventId, 5);
  assert.equal(finalDecision.shouldUpgrade('user.locked.example.test', { at: 50, partition: 'p' }), true);
  assert.equal(finalDecision.shouldUpgrade('allow-name.test', { at: 50, partition: 'p' }), false);
});

test('all event permutations converge to the same versioned decision', () => {
  const history = smallHistory();
  const permutations: EventInput[][] = [history];
  for (let i = 0; i < history.length - 1; i += 1) {
    permutations.push([...history.slice(i + 1), ...history.slice(0, i + 1)]);
    permutations.push([...history].reverse());
  }

  const expected = HSTSPolicyLibrary.replay(history, options);
  for (const permutation of permutations) {
    const library = new HSTSPolicyLibrary(options);
    for (const event of permutation) library.applyEvent(event);
    for (const host of ['deep.child.example.test', 'dup-name.test', 'user.locked.example.test']) {
      const actual = library.evaluate(host, { at: 50, partition: 'p' });
      const wanted = expected.evaluate(host, { at: 50, partition: 'p' });
      assert.equal(actual.shouldUpgrade, wanted.shouldUpgrade);
      assert.equal(actual.winner?.eventId, wanted.winner?.eventId);
    }
  }
});
