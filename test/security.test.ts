import assert from 'node:assert/strict';
import { describe, it } from 'node:test';
import { Host, SecurityError } from '../index.ts';

/* ------------------------------------------------------------------ */
/* Trusted object graph built entirely on the host side.              */
/* ------------------------------------------------------------------ */

class Vault {
  secret = 'shh';
  #pin = 4242;
  title: string;

  constructor(title: string) {
    this.title = title;
  }

  read(): string {
    return `read:${this.title}:${this.#pin}`;
  }

  append(suffix: string): string {
    this.title += suffix;
    return this.title;
  }

  self(): Vault {
    return this;
  }
}

interface TrustedApi {
  vault: Vault;
  label: string;
  ping(message: string): string;
  makeVault(title: string): Vault;
  hidden: string;
}

function buildTrustedApi(): TrustedApi {
  const vault = new Vault('root');
  return {
    vault,
    label: 'trusted-api',
    ping(message) {
      return `pong:${message}`;
    },
    makeVault(title) {
      return new Vault(title);
    },
    hidden: 'do-not-expose',
  };
}

const EXPOSE = {
  vault: { read: true, title: true, self: true },
  label: true,
  ping: true,
  makeVault: { $return: { read: true, title: true } },
} as const;

function fresh() {
  const host = new Host();
  const doc = { id: 'doc-a' };
  const trusted = buildTrustedApi();
  const same = host.wrap(trusted, doc);
  const cross = host.wrapCrossOrigin(trusted, doc, EXPOSE);
  return { host, doc, trusted, same, cross };
}

const rejects = async (fn: () => unknown): Promise<void> => {
  await assert.rejects(async () => fn(), SecurityError);
};

/* ------------------------------------------------------------------ */
/* Tests                                                               */
/* ------------------------------------------------------------------ */

describe('same-origin membrane', () => {
  it('forwards declared operations and keeps wrapper identity stable', () => {
    const { host, same, trusted } = fresh();
    assert.equal(same.label, 'trusted-api');
    assert.equal(same.ping('x'), 'pong:x');
    assert.equal(host.isWrapper(same), true);
    assert.equal(same.vault === same.vault, true);
    assert.equal(host.unwrap(same), trusted);
  });

  it('wraps nested objects and every object returned from methods', () => {
    const { host, same } = fresh();
    assert.equal(host.isWrapper(same.vault), true);
    assert.equal(same.vault.read(), 'read:root:4242');
    const returned = same.makeVault('child');
    assert.equal(host.isWrapper(returned), true);
    assert.equal(returned.read(), 'read:child:4242');
    assert.equal(host.isWrapper(same.vault.self()), true);
  });

  it('unwraps wrapper arguments before they reach trusted code', () => {
    const { host, same, trusted } = fresh();
    const child = same.makeVault('c');
    const store = { held: null as unknown };
    const api = host.wrap(
      {
        hold(value: unknown) {
          store.held = value;
        },
      },
      {},
    );
    api.hold(child);
    assert.equal(store.held, host.unwrap(child));
    assert.equal(store.held instanceof Vault, true);
    assert.notEqual(store.held, child);
    assert.equal(trusted === host.unwrap(same), true);
  });

  it('hides real prototypes', () => {
    const { same } = fresh();
    assert.equal(Object.getPrototypeOf(same), null);
    assert.equal(Object.getPrototypeOf(same.vault), null);
    assert.throws(() => Object.setPrototypeOf(same, {}), SecurityError);
  });
});

describe('cross-origin allow-list', () => {
  it('exposes only declared operations', () => {
    const { cross } = fresh();
    assert.equal(cross.label, 'trusted-api');
    assert.equal(cross.ping('hi'), 'pong:hi');
    assert.equal(cross.vault.title, 'root');
    assert.equal(cross.vault.read(), 'read:root:4242');
    assert.throws(() => cross.hidden, SecurityError);
    assert.throws(() => cross.vault.secret, SecurityError);
    assert.throws(() => cross.vault.append('x'), SecurityError);
  });

  it('hides undeclared keys from introspection traps', () => {
    const { cross } = fresh();
    assert.equal('hidden' in cross, false);
    assert.equal('secret' in cross.vault, false);
    assert.deepEqual(Object.keys(cross).sort(), ['label', 'makeVault', 'ping', 'vault']);
    assert.equal(Object.getOwnPropertyDescriptor(cross, 'hidden'), undefined);
  });

  it('is read-only: writes, deletes and defines all fail', () => {
    const { cross } = fresh();
    assert.throws(() => {
      cross.label = 'forged';
    }, SecurityError);
    assert.throws(() => {
      delete (cross as { label?: string }).label;
    }, SecurityError);
    assert.throws(() => Object.defineProperty(cross, 'x', { value: 1 }), SecurityError);
    assert.equal(cross.label, 'trusted-api');
  });

  it('wraps nested returned objects under the same policy', () => {
    const { host, cross } = fresh();
    const child = cross.makeVault('kid');
    assert.equal(host.isWrapper(child), true);
    assert.equal(child.read(), 'read:kid:4242');
    assert.throws(() => child.secret, SecurityError);
    const self = cross.vault.self();
    assert.equal(host.isWrapper(self), true);
  });

  it('Object.assign cannot copy hidden capability data', () => {
    const { cross } = fresh();
    const copy = Object.assign({}, cross);
    assert.equal('hidden' in copy, false);
    assert.equal(copy.label, 'trusted-api');
  });

  it('does not expose symbols, meta-keys or accessor functions', () => {
    const { cross } = fresh();
    const secretKey = Symbol('secret');
    assert.throws(() => (cross as Record<symbol, unknown>)[secretKey], SecurityError);
    assert.equal('$return' in cross.makeVault, false);
    assert.equal(Object.getOwnPropertyNames(cross.makeVault).includes('$return'), false);
    const descriptor = Object.getOwnPropertyDescriptor(cross, 'label');
    assert.equal(descriptor?.value, 'trusted-api');
    assert.equal(typeof descriptor?.get, 'undefined');
  });
});

describe('forged prototype attacks', () => {
  it('look-alike object with copied properties has no brand and cannot invoke methods', () => {
    const { cross } = fresh();
    const forged = Object.create(cross);
    assert.throws(() => forged.ping('x'), SecurityError);
    assert.throws(() => Object.create(cross.vault).read(), SecurityError);
  });

  it('plain object manually assigning a borrowed method is rejected', () => {
    const { cross } = fresh();
    const fake: { ping?: (m: string) => string } = {};
    fake.ping = cross.ping;
    assert.throws(() => fake.ping!('x'), SecurityError);
  });

  it('borrowing via call/apply/bind with a forged receiver is rejected', () => {
    const { cross } = fresh();
    const read = cross.vault.read;
    assert.throws(() => read.call({ title: 'evil' }), SecurityError);
    assert.throws(() => read.apply({ title: 'evil' }, []), SecurityError);
    assert.throws(() => read.bind({ title: 'evil' })(), SecurityError);
  });

  it('Reflect.apply and Reflect.get cannot bypass receiver checks', () => {
    const { cross } = fresh();
    const read = cross.vault.read;
    assert.throws(() => Reflect.apply(read, {}, []), SecurityError);
    assert.throws(() => Reflect.get(cross.vault, 'read', {}), SecurityError);
  });

  it('methods borrowed from one wrapper cannot be driven through another', () => {
    const { host, doc } = fresh();
    const a = host.wrapCrossOrigin(buildTrustedApi(), doc, EXPOSE);
    const b = host.wrapCrossOrigin(buildTrustedApi(), doc, EXPOSE);
    const stolen = a.vault.read;
    assert.throws(() => stolen.call(b.vault), SecurityError);
    assert.throws(() => (b.vault as { read: () => string }).read.call(a.vault), SecurityError);
    // Genuine receivers still work.
    assert.equal(a.vault.read().startsWith('read:root'), true);
    assert.equal(b.vault.read().startsWith('read:root'), true);
  });

  it('a hand-built Proxy cannot impersonate a branded wrapper', () => {
    const { cross } = fresh();
    const impersonator = new Proxy(
      {},
      {
        get() {
          return cross.vault.read;
        },
      },
    );
    assert.throws(() => (impersonator as { read: () => string }).read(), SecurityError);
  });

  it('a root function rejects forged this and survives call/apply from brands only', () => {
    const { host, doc, cross } = fresh();
    const fn = host.wrapCrossOrigin(
      function named(this: unknown, x: string) {
        return `ok:${x}:${this === undefined ? 'und' : 'leaked'}`;
      },
      doc,
      true,
    );
    assert.equal(fn('a'), 'ok:a:und');
    assert.throws(() => fn.call({ forged: true }, 'b'), SecurityError);
    assert.throws(() => fn.apply({ forged: true }, ['c']), SecurityError);
    // A branded receiver is permitted, yet trusted code still never receives
    // the proxy as `this` (the membrane substitutes the real/undefined target).
    assert.equal(fn.call(cross, 'd'), 'ok:d:und');
  });
});

describe('revocation', () => {
  it('revokeAll kills every outstanding wrapper; new wrappers remain valid', () => {
    const { host, same, cross, doc, trusted } = fresh();
    host.revokeAll();
    assert.throws(() => same.label, SecurityError);
    assert.throws(() => cross.vault.read(), SecurityError);
    assert.throws(() => cross.ping('x'), SecurityError);

    const replacement = host.wrap(trusted, doc);
    assert.equal(replacement.label, 'trusted-api');
  });

  it('property access and method invocation both honor the generation', () => {
    const { host, cross } = fresh();
    const read = cross.vault.read;
    host.revokeAll();
    assert.throws(() => cross.vault.title, SecurityError);
    assert.throws(() => read(), SecurityError);
  });

  it('stashed method references die with the generation even via valid receiver', () => {
    const { host, cross } = fresh();
    const vault = cross.vault;
    host.revokeAll();
    assert.throws(() => vault.read(), SecurityError);
  });

  it('revoked methods cannot be revived via call/apply with a live-looking object', () => {
    const { host, cross } = fresh();
    const read = cross.vault.read;
    const vault = cross.vault;
    host.revokeAll();
    assert.throws(() => read.call(vault), SecurityError);
    assert.throws(() => read.apply(vault, []), SecurityError);
  });
});

describe('document navigation', () => {
  it('navigation invalidates wrappers bound to that document only', () => {
    const host = new Host();
    const docA = { id: 'a' };
    const docB = { id: 'b' };
    const wA = host.wrap(buildTrustedApi(), docA);
    const wB = host.wrap(buildTrustedApi(), docB);

    host.navigate(docA);
    assert.throws(() => wA.label, SecurityError);
    assert.throws(() => wA.vault.read(), SecurityError);
    assert.equal(wB.label, 'trusted-api');
    assert.equal(wB.vault.read(), 'read:root:4242');
  });

  it('navigation combined with revocation stays fail-closed', async () => {
    const { host, doc, cross } = fresh();
    host.navigate(doc);
    await rejects(() => cross.ping('x'));
    host.revokeAll();
    await rejects(() => cross.vault.read());
  });
});

describe('capability identity', () => {
  it('scripts can never reach the WeakMaps or original targets', () => {
    const { host, cross, trusted } = fresh();
    assert.equal(host.isWrapper(trusted), false);
    assert.equal(cross.vault !== (trusted.vault as unknown), true);
    // No enumerable back-door to the target is present on the proxy.
    for (const key of Object.getOwnPropertyNames(cross)) {
      assert.notEqual(key, 'target');
    }
  });

  it('unwrapping a forged object is a no-op (host never treats it as branded)', () => {
    const { host, cross } = fresh();
    const forged = Object.create(cross);
    assert.equal(host.isWrapper(forged), false);
    assert.equal(host.unwrap(forged), forged);
  });
});
