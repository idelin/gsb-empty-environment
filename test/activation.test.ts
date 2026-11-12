import assert from 'node:assert/strict';
import { describe, test } from 'node:test';
import {
  ActivationToken,
  UserActivationService,
} from '../src/activation.ts';
import type { TrustedUserEvent } from '../src/activation.ts';
import { VirtualClock } from '../src/clock.ts';
import {
  CrossDocumentTransferError,
  TokenUnavailableError,
} from '../src/errors.ts';
import { SimulatedBrowserHost } from '../src/host.ts';

const TTL = 100;

function click(isTrusted = true): TrustedUserEvent {
  return {
    type: 'click',
    isTrusted,
    eventId: `evt-${Math.random()}`,
    timestamp: 0,
  };
}

function makeService(ttlMs = TTL): {
  clock: VirtualClock;
  service: UserActivationService;
} {
  const clock = new VirtualClock();
  const service = new UserActivationService({ ttlMs, clock });
  service.registerDocument('doc-a');
  service.registerDocument('doc-b');
  return { clock, service };
}

describe('可信事件与令牌绑定', () => {
  test('只有可信事件能武装激活，伪造事件被忽略', () => {
    const { clock, service } = makeService();

    assert.equal(service.notifyUserEvent('doc-a', click(false)), false);
    assert.equal(service.hasTransientActivation('doc-a'), false);
    assert.equal(service.currentToken('doc-a'), null);

    assert.equal(service.notifyUserEvent('doc-a', click(true)), true);
    assert.equal(service.hasTransientActivation('doc-a'), true);
    const token = service.currentToken('doc-a');
    assert.ok(token instanceof ActivationToken);
    assert.equal(token!.documentId, 'doc-a');
    assert.equal(token!.event.type, 'click');
    assert.equal(token!.issuedAt, clock.now());
    assert.equal(token!.expiresAt, clock.now() + TTL);
  });

  test('未注册文档的事件不会产生令牌', () => {
    const { service } = makeService();
    assert.equal(service.notifyUserEvent('doc-x', click(true)), false);
    assert.equal(service.hasStickyActivation('doc-x'), false);
  });
});

describe('同时打开两个窗口：消费类操作原子领取一次', () => {
  test('同一次激活只能放行一个 window.open，第二个作为弹窗拦截', () => {
    const host = new SimulatedBrowserHost(TTL);
    const opener = host.createWindow('doc-opener');
    host.dispatchEvent('doc-opener', 'click', true);

    const first = host.openWindowFrom(opener.windowId);
    const second = host.openWindowFrom(opener.windowId);

    assert.ok(first.window, 'first window opens');
    assert.equal(first.blockedReason, null);
    assert.equal(second.window, null);
    assert.equal(second.blockedReason, 'consumed');

    const token = host.activation.currentToken('doc-opener');
    assert.equal(token!.status(host.clock.now()), 'consumed');
    assert.equal(token!.consumedAt, host.clock.now());

    const granted = host.activation.ledger().filter((e) => e.result === 'granted');
    assert.equal(granted.length, 1);
    assert.equal(granted[0]!.operation, 'consume');

    host.dispatchEvent('doc-opener', 'click', true);
    const third = host.openWindowFrom(opener.windowId);
    assert.ok(third.window, 'a new gesture arms a fresh token');
  });

  test('两个窗口各自持有独立文档，激活不互相串用', () => {
    const host = new SimulatedBrowserHost(TTL);
    const w1 = host.createWindow('doc-1');
    host.createWindow('doc-2');
    host.dispatchEvent('doc-1', 'click', true);

    assert.equal(host.canOpenPopup(w1.windowId), true);
    assert.equal(
      host.activation.tryConsume('doc-2').ok,
      false,
      'doc-2 never received a gesture',
    );

    const result = host.activation.tryConsume('doc-1');
    assert.equal(result.ok, true);
    const again = host.activation.tryConsume('doc-1');
    assert.equal(again.ok, false);
    assert.equal((again as { reason: string }).reason, 'consumed');
  });

  test('微任务里再开窗口同样被拦截，且不改变时钟', () => {
    const host = new SimulatedBrowserHost(TTL);
    const opener = host.createWindow('doc-opener');
    host.dispatchEvent('doc-opener', 'click', true);

    const outcomes: Array<boolean | string> = [];
    host.clock.setMicrotask(() => {
      outcomes.push(host.openWindowFrom(opener.windowId).blockedReason ?? 'opened');
    });
    host.clock.setMicrotask(() => {
      outcomes.push(host.openWindowFrom(opener.windowId).blockedReason ?? 'opened');
    });
    const first = host.openWindowFrom(opener.windowId);
    host.clock.advance(0);

    assert.ok(first.window);
    assert.deepEqual(outcomes, ['consumed', 'consumed']);
    assert.equal(host.clock.now(), 0, 'microtasks run at the same timestamp');
  });
});

describe('期限：过期同刻与异步任务', () => {
  test('到达期限的同一刻即过期（now >= expiresAt），边界前一刻仍有效', () => {
    const { clock, service } = makeService();
    service.notifyUserEvent('doc-a', click(true));
    const token = service.currentToken('doc-a')!;

    clock.setMacrotask(() => {
      assert.equal(clock.now(), TTL - 1);
      assert.equal(token.isLive(clock.now()), true);
      assert.equal(service.hasTransientActivation('doc-a'), true);
    }, TTL - 1);
    clock.setMacrotask(() => {
      assert.equal(clock.now(), TTL);
      assert.equal(token.status(clock.now()), 'expired');
      const result = service.tryConsume('doc-a');
      assert.equal(result.ok, false);
      assert.equal((result as { reason: string }).reason, 'expired');
    }, TTL);
    clock.advanceTo(TTL);
  });

  test('TTL 内的微任务可用，跨过期限的微任务不可用且不续期', () => {
    const { clock, service } = makeService();
    service.notifyUserEvent('doc-a', click(true));

    let inside: string | null = null;
    clock.setMacrotask(() => {
      clock.setMicrotask(() => {
        inside = service.tryConsume('doc-a').ok ? 'granted' : 'blocked';
      });
    }, 10);
    let late: string | null = null;
    clock.setMacrotask(() => {
      clock.setMicrotask(() => {
        late = service.tryConsume('doc-a').ok ? 'granted' : 'blocked';
      });
    }, TTL);
    clock.advanceTo(TTL);

    assert.equal(inside, 'granted');
    assert.equal(late, 'blocked');
  });

  test('异步任务链不能延长激活：没有新事件就没有新期限', () => {
    const { clock, service } = makeService();
    service.notifyUserEvent('doc-a', click(true));

    const observations: string[] = [];
    const chain = (depth: number, delay: number): void => {
      clock.setMacrotask(() => {
        observations.push(
          service.inspect('doc-a').live ? `live@${clock.now()}` : `dead@${clock.now()}`,
        );
        if (depth > 0) {
          chain(depth - 1, delay);
        }
      }, delay);
    };
    chain(3, 40);
    clock.advanceTo(200);

    assert.deepEqual(observations, [
      'live@40',
      'live@80',
      'dead@120',
      'dead@160',
    ]);
    assert.equal(clock.pendingTaskCount(), 0);
  });

  test('新可信事件铸造全新令牌与全新期限，旧令牌被取代', () => {
    const { clock, service } = makeService();
    service.notifyUserEvent('doc-a', click(true));
    const first = service.currentToken('doc-a')!;
    clock.advance(60);
    service.notifyUserEvent('doc-a', click(true));
    const second = service.currentToken('doc-a')!;

    assert.notEqual(first.id, second.id);
    assert.equal(first.status(clock.now()), 'superseded');
    assert.equal(second.issuedAt, 60);
    assert.equal(second.expiresAt, 160);

    clock.advanceTo(160);
    assert.equal(second.status(clock.now()), 'expired');
  });
});

describe('文档导航撤销', () => {
  test('导航立即撤销在途令牌并清空粘性激活', () => {
    const { clock, service } = makeService();
    service.notifyUserEvent('doc-a', click(true));
    const token = service.currentToken('doc-a')!;
    assert.equal(service.hasStickyActivation('doc-a'), true);

    assert.equal(service.navigate('doc-a'), true);

    assert.equal(token.status(clock.now()), 'revoked');
    assert.equal(service.currentToken('doc-a'), null);
    assert.equal(service.hasTransientActivation('doc-a'), false);
    assert.equal(service.hasStickyActivation('doc-a'), false);

    const result = service.tryConsume('doc-a');
    assert.equal(result.ok, false);
    assert.equal((result as { reason: string }).reason, 'no-activation');
  });

  test('宿主导航后新文档从无激活状态开始', () => {
    const host = new SimulatedBrowserHost(TTL);
    const win = host.createWindow('old-doc');
    host.dispatchEvent('old-doc', 'click', true);

    host.navigate(win.windowId, 'new-doc');

    assert.equal(host.canOpenPopup(win.windowId), false);
    const blocked = host.openWindowFrom(win.windowId);
    assert.equal(blocked.window, null);
    assert.equal(blocked.blockedReason, 'no-activation');
  });

  test('过期之后导航不会改写终态', () => {
    const { clock, service } = makeService();
    service.notifyUserEvent('doc-a', click(true));
    const token = service.currentToken('doc-a')!;
    clock.advance(TTL);
    assert.equal(token.status(clock.now()), 'expired');

    service.navigate('doc-a');
    assert.equal(token.status(clock.now()), 'expired');
  });
});

describe('跨文档转移拒绝', () => {
  test('令牌只能由绑定文档消费，转移尝试被拒绝且不灼伤原令牌', () => {
    const { service } = makeService();
    service.notifyUserEvent('doc-a', click(true));
    const token = service.currentToken('doc-a')!;

    const rejected = service.tryConsumeToken('doc-b', token);
    assert.equal(rejected.ok, false);
    assert.equal((rejected as { reason: string }).reason, 'cross-document');
    assert.equal(token.status(0), 'active', 'failed transfer leaves the token untouched');

    const owned = service.tryConsumeToken('doc-a', token);
    assert.equal(owned.ok, true, 'the owning document can still claim it');
  });

  test('严格 API 对跨文档转移抛出类型化错误', () => {
    const { service } = makeService();
    service.notifyUserEvent('doc-a', click(true));
    const token = service.currentToken('doc-a')!;

    assert.throws(
      () => service.strictConsumeToken('doc-b', token),
      (error: unknown) => {
        assert.ok(error instanceof CrossDocumentTransferError);
        assert.match((error as Error).message, /doc-a/);
        assert.match((error as Error).message, /doc-b/);
        return true;
      },
    );

    assert.throws(
      () => service.strictConsume('doc-b'),
      (error: unknown) => {
        assert.ok(error instanceof TokenUnavailableError);
        assert.equal((error as TokenUnavailableError).reason, 'no-activation');
        return true;
      },
    );
  });

  test('窗口无法把另一个窗口的令牌当作自己的消费凭证', () => {
    const host = new SimulatedBrowserHost(TTL);
    host.createWindow('doc-1');
    host.createWindow('doc-2');
    host.dispatchEvent('doc-1', 'click', true);
    const stolen = host.activation.currentToken('doc-1')!;

    assert.equal(host.transferAttempt('doc-2', stolen), false);
    assert.equal(stolen.status(host.clock.now()), 'active');

    host.dispatchEvent('doc-2', 'click', true);
    assert.equal(host.transferAttempt('doc-2', stolen), false);
    assert.equal(
      host.activation.tryConsume('doc-2').ok,
      true,
      'doc-2 consumes its own token instead',
    );
  });
});

describe('非消费类操作只检查', () => {
  test('inspect 与 hasTransientActivation 不消耗令牌', () => {
    const { service } = makeService();
    service.notifyUserEvent('doc-a', click(true));
    const token = service.currentToken('doc-a')!;

    for (let i = 0; i < 3; i++) {
      const inspection = service.inspect('doc-a');
      assert.equal(inspection.live, true);
      assert.equal(service.hasTransientActivation('doc-a'), true);
    }
    assert.equal(token.status(0), 'active');

    const result = service.tryConsume('doc-a');
    assert.equal(result.ok, true);
    const inspection = service.inspect('doc-a');
    assert.equal(inspection.live, false);
    assert.equal(inspection.status, 'consumed');
  });

  test('粘性激活在瞬时激活过期后仍保留，导航才清除', () => {
    const { clock, service } = makeService();
    service.notifyUserEvent('doc-a', click(true));
    clock.advance(TTL);

    assert.equal(service.hasTransientActivation('doc-a'), false);
    assert.equal(service.hasStickyActivation('doc-a'), true);

    service.navigate('doc-a');
    assert.equal(service.hasStickyActivation('doc-a'), false);
  });

  test('审计账本区分检查与消费', () => {
    const { service } = makeService();
    service.notifyUserEvent('doc-a', click(true));
    service.inspect('doc-a');
    service.inspect('doc-a');
    service.tryConsume('doc-a');
    service.tryConsume('doc-a');

    const entries = service.ledger();
    assert.deepEqual(
      entries.map((entry) => [entry.operation, entry.result]),
      [
        ['inspect', 'granted'],
        ['inspect', 'granted'],
        ['consume', 'granted'],
        ['consume', 'consumed'],
        ['consume', 'consumed'],
      ],
    );
  });
});
