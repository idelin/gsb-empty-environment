import assert from 'node:assert/strict';
import { describe, it } from 'node:test';
import {
  AccessorRejectedError,
  BudgetExceededError,
  CapabilityLeakError,
  CapabilityRegistry,
  CopyError,
  ProxyRejectedError,
  SecureMessenger,
  UnsupportedValueError,
  copyMessage,
} from '../index.ts';

describe('allowed data: primitives, arrays, data records', () => {
  it('deep-codes nested records and arrays', () => {
    const source = {
      name: 'netsec',
      flags: [true, false, null],
      meta: { retries: 3, ratio: 0.5, big: 9007199254740993n },
    };

    const copy = copyMessage(source);

    assert.deepEqual(copy, source);
    assert.notEqual(copy, source);
    assert.notEqual(copy.meta, source.meta);
    assert.notEqual(copy.flags, source.flags);
    assert.equal(copy.meta.big, 9007199254740993n);
  });

  it('accepts null-prototype records', () => {
    const source = Object.create(null) as Record<string, unknown>;
    source.a = 1;
    assert.deepEqual(copyMessage(source), { a: 1 });
  });

  it('produces an independent graph', () => {
    const source = { nested: { value: 1 }, list: [1, 2] };
    const copy = copyMessage(source);

    copy.nested.value = 99;
    (copy.list as number[]).push(3);

    assert.equal(source.nested.value, 1);
    assert.deepEqual(source.list, [1, 2]);
  });
});

describe('shared subgraphs and cycles', () => {
  it('preserves aliasing', () => {
    const shared = { point: [1, 2] };
    const source = { first: shared, second: shared };

    const copy = copyMessage(source) as typeof source;

    assert.equal(copy.first, copy.second);
    assert.equal(copy.first.point, copy.second.point);
  });

  it('supports circular records and arrays', () => {
    type Node = { name: string; self?: Node; peers: Node[] };
    const node: Node = { name: 'a', peers: [] };
    node.self = node;
    node.peers.push(node);

    const copy = copyMessage(node) as Node;

    assert.equal(copy.self, copy);
    assert.equal(copy.peers[0], copy);
    assert.deepEqual(copy.peers, [copy]);
  });

  it('links shared nodes inside cycles to the same copy', () => {
    type Node = { id: number; shared?: { v: number }; next?: Node };
    const shared = { v: 7 };
    const a: Node = { id: 1, shared };
    const b: Node = { id: 2, shared };
    a.next = b;
    b.next = a;

    const copy = copyMessage(a) as Node;

    assert.equal(copy.next?.next, copy);
    assert.equal(copy.shared, copy.next?.shared);
    assert.equal(copy.shared?.v, 7);
  });
});

describe('proxy rejection happens before any trap fires', () => {
  it('never invokes traps on a get/set/ownKeys proxy', () => {
    let trapCalls = 0;
    const trap = () => {
      trapCalls += 1;
      return true;
    };
    const proxied = new Proxy(
      { hidden: 'x' },
      {
        get: trap,
        set: trap,
        has: trap,
        ownKeys: trap,
        getOwnPropertyDescriptor: trap,
        getPrototypeOf: trap,
      },
    );

    assert.throws(() => copyMessage(proxied), ProxyRejectedError);
    assert.throws(
      () => copyMessage({ wrapper: proxied }),
      ProxyRejectedError,
    );
    assert.throws(
      () => copyMessage([proxied]),
      ProxyRejectedError,
    );
    assert.equal(trapCalls, 0);
  });

  it('rejects revoked proxies', () => {
    const { proxy, revoke } = Proxy.revocable({}, {});
    revoke();
    assert.throws(() => copyMessage(proxy), ProxyRejectedError);
  });
});

describe('capability handles never cross the boundary', () => {
  it('rejects a registered capability at the root', () => {
    const registry = new CapabilityRegistry();
    const capability = { spawn: () => 'child-process' };
    registry.register(capability);

    assert.throws(
      () => copyMessage(capability, { registry }),
      CapabilityLeakError,
    );
  });

  it('rejects a capability reached through a data property (descriptor check)', () => {
    const registry = new CapabilityRegistry();
    const capability = { fd: 4 };
    registry.register(capability);
    const source = { safe: 'value', nested: { handle: capability } };

    assert.throws(
      () => copyMessage(source, { registry }),
      CapabilityLeakError,
    );
  });

  it('rejects capabilities embedded in arrays', () => {
    const registry = new CapabilityRegistry();
    const capability = function privileged() {};
    registry.register(capability);

    assert.throws(
      () => copyMessage(['a', capability], { registry }),
      CapabilityLeakError,
    );
  });
});

describe('accessor and method inputs are rejected, never executed', () => {
  it('rejects accessor properties without invoking the getter', () => {
    let getterCalls = 0;
    const source = {};
    Object.defineProperty(source, 'secret', {
      enumerable: true,
      get() {
        getterCalls += 1;
        return 'leak';
      },
      set() {},
    });

    assert.throws(() => copyMessage(source), AccessorRejectedError);
    assert.equal(getterCalls, 0);
  });

  it('rejects methods and functions', () => {
    let methodCalls = 0;
    const source = {
      run() {
        methodCalls += 1;
      },
    };

    assert.throws(() => copyMessage(source), UnsupportedValueError);
    assert.throws(() => copyMessage(() => 1), UnsupportedValueError);
    assert.equal(methodCalls, 0);
  });

  it('rejects non-plain objects and exotic built-ins', () => {
    assert.throws(() => copyMessage(new Date()), UnsupportedValueError);
    assert.throws(() => copyMessage(new Map([['a', 1]])), UnsupportedValueError);
    assert.throws(() => copyMessage(/regex/), UnsupportedValueError);
    class Instance {
      field = 1;
    }
    assert.throws(() => copyMessage(new Instance()), UnsupportedValueError);
  });

  it('rejects non-enumerable and symbol-keyed properties fail-closed', () => {
    const hidden = { visible: 1 };
    Object.defineProperty(hidden, 'locked', {
      enumerable: false,
      value: 2,
    });
    assert.throws(() => copyMessage(hidden), UnsupportedValueError);

    const symboled = { visible: 1, [Symbol('k')]: 2 };
    assert.throws(() => copyMessage(symboled), UnsupportedValueError);
  });
});

describe('node and byte budgets', () => {
  it('enforces the node budget while sharing repeated references', () => {
    const source = { a: { x: 1 }, b: { y: 2 }, c: { z: 3 } };

    assert.throws(
      () => copyMessage(source, { budgets: { maxNodes: 3 } }),
      BudgetExceededError,
    );
    assert.doesNotThrow(() =>
      copyMessage(source, { budgets: { maxNodes: 4 } }),
    );
  });

  it('enforces the byte budget on strings', () => {
    assert.throws(
      () => copyMessage({ payload: 'x'.repeat(100) }, {
        budgets: { maxBytes: 64 },
      }),
      BudgetExceededError,
    );
  });

  it('enforces budgets on cyclic input as well', () => {
    type Ring = { next?: Ring };
    const ring: Ring = {};
    ring.next = ring;

    assert.throws(
      () => copyMessage(ring, { budgets: { maxNodes: 0 } }),
      BudgetExceededError,
    );
  });
});

describe('SecureMessenger all-or-nothing delivery', () => {
  it('delivers only a fully copied, isolated message', () => {
    const registry = new CapabilityRegistry();
    const messenger = new SecureMessenger({ registry });
    const source = { ok: true, list: [1, { deep: 'value' }] };
    const delivered: unknown[] = [];

    messenger.send(source, (message) => delivered.push(message));

    assert.equal(delivered.length, 1);
    assert.notEqual(delivered[0], source);
    assert.deepEqual(delivered[0], source);
  });

  it('never calls the send callback for proxied, cyclic-over-budget, or leaking input', () => {
    const registry = new CapabilityRegistry();
    const capability = { token: 'root-only' };
    registry.register(capability);
    const messenger = new SecureMessenger({
      registry,
      budgets: { maxNodes: 1 },
    });

    let sendCalls = 0;
    const callback = () => {
      sendCalls += 1;
    };

    const proxied = new Proxy({ a: 1 }, { get: () => 'trap' });
    const cyclic = { child: {} as { back?: unknown } };
    cyclic.child.back = cyclic;

    assert.throws(() => messenger.send(proxied, callback), ProxyRejectedError);
    assert.throws(
      () => messenger.send({ handle: capability }, callback),
      CapabilityLeakError,
    );
    assert.throws(() => messenger.send(cyclic, callback), BudgetExceededError);
    assert.throws(
      () => messenger.send({ get bad() { return 1; } }, callback),
      AccessorRejectedError,
    );

    assert.equal(sendCalls, 0);
  });

  it('exposes CopyError as the common error base', () => {
    const registry = new CapabilityRegistry();
    const capability = {};
    registry.register(capability);

    assert.throws(
      () => copyMessage(capability, { registry }),
      CopyError,
    );
    assert.throws(
      () => copyMessage(new Proxy({}, {}), { registry }),
      CopyError,
    );
  });
});
