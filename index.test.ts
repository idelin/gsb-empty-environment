import test from 'node:test';
import assert from 'node:assert/strict';
import {
  NavigationHost,
  NO_RESTRICTIONS,
  PopupBlockedError,
  StaleDocumentError,
  restrictionsEqual,
  sameOrigin,
  unionRestrictions,
  type NavEvent,
  type Restrictions,
} from './index.ts';

const ALL: Restrictions = { scripts: true, popups: true, preserveOrigin: true };

function eventsOfType<T extends NavEvent['type']>(
  host: NavigationHost,
  type: T,
): Extract<NavEvent, { type: T }>[] {
  return host.events.filter((event) => event.type === type) as Extract<NavEvent, { type: T }>[];
}

test('unionRestrictions 对三项限制分别取并', () => {
  assert.deepEqual(unionRestrictions(NO_RESTRICTIONS, NO_RESTRICTIONS), NO_RESTRICTIONS);
  assert.deepEqual(
    unionRestrictions({ scripts: true, popups: false, preserveOrigin: false }, ALL),
    ALL,
  );
  assert.deepEqual(
    unionRestrictions(
      { scripts: true, popups: false, preserveOrigin: true },
      { scripts: false, popups: true, preserveOrigin: false },
    ),
    ALL,
  );
  assert.ok(restrictionsEqual(unionRestrictions(ALL, ALL), ALL));
});

test('nested updates: child declarations union with parent effective restrictions', () => {
  const host = new NavigationHost();
  const root = host.createRoot('https://a.example/');
  root.declareRestrictions({ scripts: true });
  root.navigate();

  const child = root.createChild('https://a.example/frame');
  child.declareRestrictions({ popups: true });
  child.navigate();

  assert.deepEqual(child.snapshot().effective, {
    scripts: true,
    popups: true,
    preserveOrigin: false,
  });
  assert.deepEqual(root.snapshot().effective, {
    scripts: true,
    popups: false,
    preserveOrigin: false,
  });

  const grandchild = child.createChild();
  grandchild.navigate();
  assert.deepEqual(grandchild.snapshot().effective, {
    scripts: true,
    popups: true,
    preserveOrigin: false,
  });

  const types = host.events.map((event) => event.type);
  assert.ok(types.indexOf('declared') < types.indexOf('navigated'));
  const navigated = eventsOfType(host, 'navigated');
  assert.equal(navigated.length, 3);
  assert.deepEqual(
    navigated.map((event) => event.doc.number),
    [root.number, child.number, grandchild.number],
  );
});

test('empty document inherits parent effective restrictions and origin', () => {
  const host = new NavigationHost();
  const parent = host.createRoot('https://parent.example/');
  parent.declareRestrictions({ scripts: true });
  parent.navigate();

  const empty = parent.createChild();
  assert.deepEqual(empty.snapshot().effective, {
    scripts: true,
    popups: false,
    preserveOrigin: false,
  });
  assert.ok(sameOrigin(empty.snapshot().origin, parent.snapshot().origin));
});

test('empty document gets a fresh opaque origin when origin preservation is forbidden', () => {
  const host = new NavigationHost();
  const parent = host.createRoot();
  parent.declareRestrictions({ preserveOrigin: true });
  parent.navigate();

  const first = parent.createChild();
  const second = parent.createChild();
  for (const doc of [parent, first, second]) {
    assert.equal(doc.snapshot().origin.kind, 'opaque');
    assert.equal(doc.snapshot().effective.preserveOrigin, true);
  }
  assert.ok(!sameOrigin(first.snapshot().origin, parent.snapshot().origin));
  assert.ok(!sameOrigin(first.snapshot().origin, second.snapshot().origin));
  assert.ok(!sameOrigin(second.snapshot().origin, parent.snapshot().origin));
});

test('deferred navigation: declarations apply only on the next navigation', () => {
  const host = new NavigationHost();
  const doc = host.createRoot('https://x.example/');
  assert.deepEqual(doc.snapshot().effective, NO_RESTRICTIONS);

  doc.declareRestrictions({ scripts: true });
  assert.deepEqual(doc.snapshot().effective, NO_RESTRICTIONS);
  assert.equal(host.events.at(-1)?.type, 'declared');

  doc.navigate();
  assert.equal(doc.snapshot().effective.scripts, true);
  assert.equal(host.events.at(-1)?.type, 'navigated');

  doc.navigate();
  assert.equal(doc.snapshot().effective.scripts, true);

  let ran = 0;
  doc.navigate(undefined, () => {
    ran += 1;
  });
  assert.equal(ran, 0);
  host.drain();
  assert.equal(ran, 1);
  assert.equal(eventsOfType(host, 'callback').length, 1);
});

test('derived popup inherits restrictions captured at creation time', () => {
  const host = new NavigationHost();
  const opener = host.createRoot('https://o.example/');
  opener.declareRestrictions({ scripts: true });
  opener.navigate();

  const popup = opener.openPopup();
  assert.deepEqual(popup.snapshot().effective, {
    scripts: true,
    popups: false,
    preserveOrigin: false,
  });

  const opened = eventsOfType(host, 'popup-opened');
  assert.equal(opened.length, 1);
  assert.deepEqual(opened[0].inherited, popup.snapshot().effective);
  assert.deepEqual(opened[0].opener, opener.ref);
  assert.deepEqual(opened[0].popup, popup.ref);

  opener.declareRestrictions({ popups: true });
  opener.navigate();
  assert.equal(popup.snapshot().effective.popups, false);
  assert.equal(opener.snapshot().effective.popups, true);

  assert.throws(() => opener.openPopup(), PopupBlockedError);
  assert.equal(eventsOfType(host, 'popup-opened').length, 1);
});

test('unmount invalidates pending navigation callbacks', () => {
  const host = new NavigationHost();
  const doc = host.createRoot();
  let ran = 0;
  doc.navigate(undefined, () => {
    ran += 1;
  });
  doc.unmount();
  host.drain();

  assert.equal(ran, 0);
  assert.equal(eventsOfType(host, 'callback').length, 0);
  const dropped = eventsOfType(host, 'callback-dropped');
  assert.equal(dropped.length, 1);
  assert.equal(dropped[0].reason, 'unmounted');
  assert.deepEqual(dropped[0].doc, doc.ref);
});

test('callbacks superseded by a newer navigation are dropped', () => {
  const host = new NavigationHost();
  const doc = host.createRoot();
  const ran: number[] = [];
  doc.navigate(undefined, () => ran.push(1));
  doc.navigate(undefined, () => ran.push(2));
  host.drain();

  assert.deepEqual(ran, [2]);
  const dropped = eventsOfType(host, 'callback-dropped');
  assert.equal(dropped.length, 1);
  assert.equal(dropped[0].reason, 'superseded');
  assert.equal(dropped[0].navigation, 1);
  assert.equal(eventsOfType(host, 'callback').length, 1);
});

test('document number reuse: generation separates old handles and callbacks', () => {
  const host = new NavigationHost();
  host.createRoot();
  const old = host.createRoot();
  assert.equal(old.number, 1);

  let ran = 0;
  old.navigate(undefined, () => {
    ran += 1;
  });
  old.unmount();

  const reused = host.createRoot('https://reused.example/');
  assert.equal(reused.number, old.number);
  assert.notEqual(reused.generation, old.generation);

  host.drain();
  assert.equal(ran, 0);
  const dropped = eventsOfType(host, 'callback-dropped');
  assert.equal(dropped.length, 1);
  assert.equal(dropped[0].reason, 'unmounted');
  assert.deepEqual(dropped[0].doc, old.ref);

  assert.throws(() => old.navigate(), StaleDocumentError);
  assert.throws(() => old.declareRestrictions({ scripts: true }), StaleDocumentError);
  assert.throws(() => old.snapshot(), StaleDocumentError);
  assert.throws(() => old.unmount(), StaleDocumentError);

  reused.navigate(undefined, () => {
    ran += 1;
  });
  host.drain();
  assert.equal(ran, 1);
  const delivered = eventsOfType(host, 'callback');
  assert.equal(delivered.length, 1);
  assert.deepEqual(delivered[0].doc, reused.ref);
});

test('opaque origins are unique across the whole host', () => {
  const host = new NavigationHost();
  const docs = [host.createRoot(), host.createRoot(), host.createRoot()];
  for (const doc of docs) {
    doc.declareRestrictions({ preserveOrigin: true });
    doc.navigate();
  }
  const origins = docs.map((doc) => doc.snapshot().origin);
  for (let i = 0; i < origins.length; i += 1) {
    for (let j = 0; j < origins.length; j += 1) {
      assert.equal(sameOrigin(origins[i], origins[j]), i === j);
    }
  }
});
