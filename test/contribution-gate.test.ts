import assert from 'node:assert/strict';
import { describe, it } from 'node:test';
import { createContributionGate } from '../index.ts';
import type {
  HistogramContribution,
  PreparedReport,
  ReportSink,
} from '../index.ts';

const ALLOWED_ORIGIN = 'https://aggsvc.example';
const OTHER_ALLOWED_ORIGIN = 'https://backup-aggsvc.example';
const CTX = 'ctx-123';

interface Harness {
  sent: PreparedReport[];
  sink: ReportSink;
  gate: ReturnType<typeof createContributionGate>['gate'];
  drain: ReturnType<typeof createContributionGate>['drainReport'];
}

function makeGate(thirdPartyCookiesEnabled: boolean, origins = [ALLOWED_ORIGIN]): Harness {
  const sent: PreparedReport[] = [];
  const handle = createContributionGate({
    allowedCoordinatorOrigins: origins,
    capabilities: { thirdPartyCookiesEnabled },
  });
  return {
    sent,
    sink: { send: (report) => sent.push(report) },
    gate: handle.gate,
    drain: handle.drainReport,
  };
}

function contribution(overrides: Partial<HistogramContribution> = {}): HistogramContribution {
  return { bucket: 1n, value: 1, ...overrides };
}

function options(overrides: Record<string, unknown> = {}) {
  return {
    aggregationCoordinatorOrigin: ALLOWED_ORIGIN,
    contextId: CTX,
    ...overrides,
  };
}

describe('coordinator origin allowlist', () => {
  it('rejects an unregistered coordinator with TypeError and writes nothing', () => {
    const h = makeGate(true);
    assert.throws(
      () =>
        h.gate.contributeToHistogram(
          contribution(),
          options({ aggregationCoordinatorOrigin: 'https://evil.example' }),
        ),
      TypeError,
    );
    assert.equal(h.drain(CTX, h.sink), null);
    assert.deepEqual(h.sent, []);
  });

  it('rejects non-http(s) origins even if the raw string appears in the list', () => {
    assert.throws(
      () =>
        createContributionGate({
          allowedCoordinatorOrigins: ['javascript:alert(1)'],
        }),
      TypeError,
    );
  });

  it('normalizes origins so trailing paths resolve to the registered origin', () => {
    const h = makeGate(true);
    h.gate.contributeToHistogram(
      contribution(),
      options({ aggregationCoordinatorOrigin: `${ALLOWED_ORIGIN}/evil-path` }),
    );
    const record = h.drain(CTX, h.sink);
    assert.equal(record?.aggregationCoordinatorOrigin, ALLOWED_ORIGIN);
  });

  it('rejects an origin whose host is a suffix lookalike', () => {
    const h = makeGate(true);
    assert.throws(
      () =>
        h.gate.contributeToHistogram(
          contribution(),
          options({ aggregationCoordinatorOrigin: 'https://aggsvc.example.evil.test' }),
        ),
      TypeError,
    );
  });

  it('accepts any registered origin and records it on the report', () => {
    const h = makeGate(true, [ALLOWED_ORIGIN, OTHER_ALLOWED_ORIGIN]);
    h.gate.contributeToHistogram(
      contribution(),
      options({ aggregationCoordinatorOrigin: OTHER_ALLOWED_ORIGIN }),
    );
    const record = h.drain(CTX, h.sink);
    assert.equal(record?.aggregationCoordinatorOrigin, OTHER_ALLOWED_ORIGIN);
  });
});

describe('bucket and value validation', () => {
  it('rejects negative values', () => {
    const h = makeGate(true);
    assert.throws(
      () => h.gate.contributeToHistogram(contribution({ value: -1 }), options()),
      TypeError,
    );
  });

  it('rejects fractional values', () => {
    const h = makeGate(true);
    for (const bad of [1.5, -0.25, Number.NaN, Number.POSITIVE_INFINITY]) {
      assert.throws(
        () => h.gate.contributeToHistogram(contribution({ value: bad }), options()),
        TypeError,
      );
    }
  });

  it('rejects negative and out-of-range buckets', () => {
    const h = makeGate(true);
    assert.throws(
      () => h.gate.contributeToHistogram(contribution({ bucket: -1n }), options()),
      TypeError,
    );
    assert.throws(
      () =>
        h.gate.contributeToHistogram(
          contribution({ bucket: 1n << 128n }),
          options(),
        ),
      TypeError,
    );
  });

  it('rejects number buckets because they cannot represent full 128-bit range', () => {
    const h = makeGate(true);
    assert.throws(
      () =>
        h.gate.contributeToHistogram(
          { bucket: 42 as unknown as bigint, value: 1 },
          options(),
        ),
      TypeError,
    );
  });

  it('accepts boundary values bucket 0 and 2^128 - 1', () => {
    const h = makeGate(true);
    h.gate.contributeToHistogram(
      [
        contribution({ bucket: 0n, value: 0 }),
        contribution({ bucket: (1n << 128n) - 1n, value: 2 }),
      ],
      options(),
    );
    const record = h.drain(CTX, h.sink);
    assert.equal(record?.contributionCount, 2);
  });
});

describe('per-context report capacity (max 20)', () => {
  it('drops the 21st contribution while keeping the first 20', () => {
    const h = makeGate(true);
    const twentyOne = Array.from({ length: 21 }, (_unused, index) =>
      contribution({ bucket: BigInt(index + 1) }),
    );
    h.gate.contributeToHistogram(twentyOne, options());
    const record = h.drain(CTX, h.sink);
    assert.equal(record?.contributionCount, 20);
    assert.deepEqual(
      h.sent[0].contributions.map((entry) => entry.bucket),
      Array.from({ length: 20 }, (_unused, index) => BigInt(index + 1)),
    );
  });

  it('accumulates across calls under one context and truncates overflow silently', () => {
    const h = makeGate(true);
    h.gate.contributeToHistogram(
      Array.from({ length: 15 }, (_unused, index) =>
        contribution({ bucket: BigInt(index + 1) }),
      ),
      options(),
    );
    h.gate.contributeToHistogram(
      Array.from({ length: 10 }, (_unused, index) =>
        contribution({ bucket: BigInt(100 + index) }),
      ),
      options(),
    );
    const record = h.drain(CTX, h.sink);
    assert.equal(record?.contributionCount, 20);
    assert.equal(h.sent[0].contributions.at(-1)?.bucket, 104n);
  });

  it('keeps separate reports for separate context ids', () => {
    const h = makeGate(true);
    h.gate.contributeToHistogram(contribution(), options({ contextId: 'a' }));
    h.gate.contributeToHistogram(contribution(), options({ contextId: 'b' }));
    assert.equal(h.drain('a', h.sink)?.contributionCount, 1);
    assert.equal(h.drain('b', h.sink)?.contributionCount, 1);
  });
});

describe('context_id byte limit (64 bytes)', () => {
  it('accepts a 64-byte ASCII id and rejects a 65-byte id', () => {
    const h = makeGate(true);
    const sixtyFour = 'a'.repeat(64);
    h.gate.contributeToHistogram(contribution(), options({ contextId: sixtyFour }));
    assert.equal(h.drain(sixtyFour, h.sink)?.contributionCount, 1);

    assert.throws(
      () =>
        h.gate.contributeToHistogram(
          contribution(),
          options({ contextId: 'a'.repeat(65) }),
        ),
      TypeError,
    );
  });

  it('counts UTF-8 bytes, so 22 three-byte characters (66 bytes) are rejected', () => {
    const h = makeGate(true);
    assert.throws(
      () =>
        h.gate.contributeToHistogram(
          contribution(),
          options({ contextId: '中'.repeat(22) }),
        ),
      TypeError,
    );
  });
});

describe('filtering_id', () => {
  it('rejects values outside [0, 255]', () => {
    const h = makeGate(true);
    for (const bad of [-1, 256, 1.5, '7']) {
      assert.throws(
        () =>
          h.gate.contributeToHistogram(
            contribution(),
            options({ filteringId: bad }),
          ),
        TypeError,
      );
    }
  });

  it('rejects conflicting filtering_ids within one report', () => {
    const h = makeGate(true);
    h.gate.contributeToHistogram(
      contribution({ filteringId: 3 }),
      options(),
    );
    assert.throws(
      () =>
        h.gate.contributeToHistogram(
          contribution({ filteringId: 4 }),
          options(),
        ),
      TypeError,
    );
    // 冲突调用整体不写入，报告仍只含第一条且 filtering_id 为 3。
    const record = h.drain(CTX, h.sink);
    assert.equal(record?.contributionCount, 1);
    assert.equal(record?.filteringId, 3);
  });

  it('rejects a mismatch between report-level and contribution-level filtering_id', () => {
    const h = makeGate(true);
    assert.throws(
      () =>
        h.gate.contributeToHistogram(
          [contribution({ filteringId: 1 }), contribution()],
          options({ filteringId: 2 }),
        ),
      TypeError,
    );
    assert.equal(h.drain(CTX, h.sink), null);
  });

  it('defaults to 0 and pins the report to the first-seen value', () => {
    const h = makeGate(true);
    h.gate.contributeToHistogram(
      [contribution(), contribution()],
      options(),
    );
    const record = h.drain(CTX, h.sink);
    assert.equal(record?.filteringId, 0);
    assert.equal(h.sent[0].contributions.every((entry) => entry.filteringId === 0), true);
  });
});

describe('debug mode and third-party cookie gating', () => {
  it('ignores debug mode when third-party cookies are unavailable', () => {
    const h = makeGate(false);
    h.gate.contributeToHistogram(
      contribution(),
      options({ debugMode: true }),
    );
    const record = h.drain(CTX, h.sink);
    assert.equal(record?.debugMode, false);
  });

  it('enables debug mode when third-party cookies are available and latches it', () => {
    const h = makeGate(true);
    h.gate.contributeToHistogram(
      contribution(),
      options({ debugMode: true }),
    );
    h.gate.contributeToHistogram(contribution(), options());
    assert.equal(h.drain(CTX, h.sink)?.debugMode, true);
  });

  it('defaults cookies to unavailable when capabilities are omitted', () => {
    const handle = createContributionGate({
      allowedCoordinatorOrigins: [ALLOWED_ORIGIN],
    });
    handle.gate.contributeToHistogram(
      contribution(),
      options({ debugMode: true }),
    );
    assert.equal(handle.drainReport(CTX, { send: () => undefined })?.debugMode, false);
  });
});

describe('worklet isolation and failure atomicity', () => {
  it('exposes no way to read contributions through the worklet gate', () => {
    const h = makeGate(true);
    h.gate.contributeToHistogram(contribution(), options());
    assert.deepEqual(Object.keys(h.gate), ['contributeToHistogram']);
    assert.equal(Object.isFrozen(h.gate), true);
    assert.equal(
      (h.gate as unknown as { drainReport?: unknown }).drainReport,
      undefined,
    );
  });

  it('freezes drained report data and removes it from the gate afterwards', () => {
    const h = makeGate(true);
    h.gate.contributeToHistogram(contribution(), options());
    h.drain(CTX, h.sink);
    assert.equal(h.drain(CTX, h.sink), null);
    assert.throws(
      () => {
        'use strict';
        (h.sent[0] as unknown as { debugMode: boolean }).debugMode = true;
      },
      TypeError,
    );
  });

  it('rolls back the whole batch when any single contribution is invalid', () => {
    const h = makeGate(true);
    assert.throws(
      () =>
        h.gate.contributeToHistogram(
          [contribution(), contribution({ value: -9 }), contribution()],
          options(),
        ),
      TypeError,
    );
    assert.equal(h.drain(CTX, h.sink), null);
  });

  it('defends against caller mutation after submission', () => {
    const h = makeGate(true);
    const mutable = contribution({ bucket: 7n, value: 1 });
    h.gate.contributeToHistogram(mutable, options());
    mutable.bucket = 999n;
    mutable.value = 5;
    h.drain(CTX, h.sink);
    assert.equal(h.sent[0].contributions[0].bucket, 7n);
    assert.equal(h.sent[0].contributions[0].value, 1);
  });
});
