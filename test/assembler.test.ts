import { FragmentAssembler, AddResult } from '../src/assembler';

declare const process: { exit(code: number): void };

const assert = {
  strictEqual(actual: unknown, expected: unknown, message?: string): void {
    if (!Object.is(actual, expected)) {
      throw new Error(message ?? `expected ${String(expected)}, got ${String(actual)}`);
    }
  },
  notStrictEqual(actual: unknown, expected: unknown, message?: string): void {
    if (Object.is(actual, expected)) {
      throw new Error(message ?? `expected values to differ, both are ${String(actual)}`);
    }
  },
  deepStrictEqual(actual: unknown, expected: unknown, message?: string): void {
    if (JSON.stringify(actual) !== JSON.stringify(expected)) {
      throw new Error(message ?? `expected ${JSON.stringify(expected)}, got ${JSON.stringify(actual)}`);
    }
  },
  ok(value: unknown, message?: string): void {
    if (!value) {
      throw new Error(message ?? `expected truthy value, got ${String(value)}`);
    }
  },
  match(value: string, pattern: RegExp, message?: string): void {
    if (!pattern.test(value)) {
      throw new Error(message ?? `expected ${value} to match ${String(pattern)}`);
    }
  },
};

let failures = 0;

function test(name: string, fn: () => void): void {
  try {
    fn();
    console.log(`ok - ${name}`);
  } catch (error) {
    failures++;
    console.error(`not ok - ${name}`);
    console.error(error);
  }
}

class ManualClock {
  current = 1_000_000;
  now = (): number => this.current;
  advance(ms: number): void {
    this.current += ms;
  }
}

function bytes(text: string): Uint8Array {
  return Uint8Array.from(text, (ch) => ch.charCodeAt(0));
}

function text(data: Uint8Array): string {
  return String.fromCharCode(...data);
}

function makeAssembler(clock: ManualClock, maxBufferedBytes = 1024, timeoutMs = 1000): FragmentAssembler {
  return new FragmentAssembler({
    maxBufferedBytes,
    generationTimeoutMs: timeoutMs,
    now: clock.now,
  });
}

test('empty message completes immediately with empty payload', () => {
  const clock = new ManualClock();
  const assembler = makeAssembler(clock);
  const result = assembler.addFragment({ messageId: 'm', totalLength: 0, offset: 0, data: new Uint8Array(0) });
  assert.strictEqual(result.status, 'complete');
  assert.strictEqual((result as { data: Uint8Array }).data.length, 0);
  assert.strictEqual(assembler.inFlightCount, 0);
  assert.strictEqual(assembler.usedBytes, 0);
});

test('empty message id can be reused immediately for a new message', () => {
  const clock = new ManualClock();
  const assembler = makeAssembler(clock);
  assembler.addFragment({ messageId: 'm', totalLength: 0, offset: 0, data: new Uint8Array(0) });
  const result = assembler.addFragment({ messageId: 'm', totalLength: 3, offset: 0, data: bytes('abc') });
  assert.strictEqual(result.status, 'complete');
  assert.strictEqual(text((result as { data: Uint8Array }).data), 'abc');
});

test('out-of-order fragments assemble a complete message', () => {
  const clock = new ManualClock();
  const assembler = makeAssembler(clock);
  assert.strictEqual(assembler.addFragment({ messageId: 'm', totalLength: 6, offset: 3, data: bytes('def') }).status, 'accepted');
  const result = assembler.addFragment({ messageId: 'm', totalLength: 6, offset: 0, data: bytes('abc') });
  assert.strictEqual(result.status, 'complete');
  assert.strictEqual(text((result as { data: Uint8Array }).data), 'abcdef');
  assert.strictEqual(assembler.usedBytes, 0);
});

test('consistent overlapping retransmit at boundary is accepted', () => {
  const clock = new ManualClock();
  const assembler = makeAssembler(clock);
  assert.strictEqual(assembler.addFragment({ messageId: 'm', totalLength: 6, offset: 0, data: bytes('abcd') }).status, 'accepted');
  const result = assembler.addFragment({ messageId: 'm', totalLength: 6, offset: 2, data: bytes('cdef') });
  assert.strictEqual(result.status, 'complete');
  assert.strictEqual(text((result as { data: Uint8Array }).data), 'abcdef');
});

test('fully covered retransmit is reported as duplicate', () => {
  const clock = new ManualClock();
  const assembler = makeAssembler(clock);
  assembler.addFragment({ messageId: 'm', totalLength: 4, offset: 0, data: bytes('abc') });
  const result = assembler.addFragment({ messageId: 'm', totalLength: 4, offset: 1, data: bytes('bc') });
  assert.strictEqual(result.status, 'duplicate');
  const done = assembler.addFragment({ messageId: 'm', totalLength: 4, offset: 3, data: bytes('d') });
  assert.strictEqual(done.status, 'complete');
  assert.strictEqual(text((done as { data: Uint8Array }).data), 'abcd');
});

test('conflicting overlap quarantines the whole message', () => {
  const clock = new ManualClock();
  const assembler = makeAssembler(clock);
  assembler.addFragment({ messageId: 'm', totalLength: 6, offset: 0, data: bytes('abcd') });
  const conflict = assembler.addFragment({ messageId: 'm', totalLength: 6, offset: 2, data: bytes('cXef') });
  assert.strictEqual(conflict.status, 'conflict');
  assert.ok(assembler.isQuarantined('m'));

  const later = assembler.addFragment({ messageId: 'm', totalLength: 6, offset: 4, data: bytes('ef') });
  assert.strictEqual(later.status, 'quarantined');
  assert.strictEqual(assembler.usedBytes, 0, 'quarantine releases buffered memory');
});

test('quarantine expires and a new generation may enter after timeout', () => {
  const clock = new ManualClock();
  const assembler = makeAssembler(clock, 1024, 1000);
  assembler.addFragment({ messageId: 'm', totalLength: 4, offset: 0, data: bytes('ab') });
  assembler.addFragment({ messageId: 'm', totalLength: 4, offset: 0, data: bytes('XX') });
  assert.ok(assembler.isQuarantined('m'));

  clock.advance(999);
  assert.strictEqual(
    assembler.addFragment({ messageId: 'm', totalLength: 4, offset: 0, data: bytes('ab') }).status,
    'quarantined',
  );

  clock.advance(1);
  const first = assembler.addFragment({ messageId: 'm', totalLength: 4, offset: 0, data: bytes('wxyz'.slice(0, 2)) });
  assert.strictEqual(first.status, 'accepted');
  const done = assembler.addFragment({ messageId: 'm', totalLength: 4, offset: 2, data: bytes('yz') });
  assert.strictEqual(done.status, 'complete');
  assert.strictEqual(text((done as { data: Uint8Array }).data), 'wxyz');
});

test('late fragment with stale totalLength cannot join the new generation', () => {
  const clock = new ManualClock();
  const assembler = makeAssembler(clock, 1024, 1000);
  assembler.addFragment({ messageId: 'm', totalLength: 8, offset: 0, data: bytes('aaaa') });

  clock.advance(1000);
  assert.strictEqual(assembler.addFragment({ messageId: 'm', totalLength: 4, offset: 0, data: bytes('bb') }).status, 'accepted');

  const late = assembler.addFragment({ messageId: 'm', totalLength: 8, offset: 4, data: bytes('cccc') });
  assert.strictEqual(late.status, 'rejected');
  assert.match((late as { reason: string }).reason, /totalLength/);

  const done = assembler.addFragment({ messageId: 'm', totalLength: 4, offset: 2, data: bytes('bb') });
  assert.strictEqual(done.status, 'complete');
  assert.strictEqual(text((done as { data: Uint8Array }).data), 'bbbb');
});

test('late fragment tagged with an old generation is rejected, never mixed in', () => {
  const clock = new ManualClock();
  const assembler = makeAssembler(clock, 1024, 1000);
  assembler.addFragment({ messageId: 'm', totalLength: 4, offset: 0, data: bytes('old!'), generation: 1 });

  clock.advance(1000);
  assembler.addFragment({ messageId: 'm', totalLength: 4, offset: 0, data: bytes('ne'), generation: 2 });

  const late = assembler.addFragment({ messageId: 'm', totalLength: 4, offset: 2, data: bytes('d!'), generation: 1 });
  assert.strictEqual(late.status, 'rejected');
  assert.match((late as { reason: string }).reason, /stale generation/);

  const done = assembler.addFragment({ messageId: 'm', totalLength: 4, offset: 2, data: bytes('w!'), generation: 2 });
  assert.strictEqual(done.status, 'complete');
  assert.strictEqual(text((done as { data: Uint8Array }).data), 'new!');
});

test('untagged late fragment overlapping new bytes quarantines instead of corrupting', () => {
  const clock = new ManualClock();
  const assembler = makeAssembler(clock, 1024, 1000);
  assembler.addFragment({ messageId: 'm', totalLength: 4, offset: 0, data: bytes('old!') });

  clock.advance(1000);
  assembler.addFragment({ messageId: 'm', totalLength: 4, offset: 0, data: bytes('ne') });

  const conflict = assembler.addFragment({ messageId: 'm', totalLength: 4, offset: 0, data: bytes('old!') });
  assert.strictEqual(conflict.status, 'conflict');
  assert.ok(assembler.isQuarantined('m'));
});

test('incomplete generation never completes after timeout resets state', () => {
  const clock = new ManualClock();
  const assembler = makeAssembler(clock, 1024, 1000);
  assembler.addFragment({ messageId: 'm', totalLength: 4, offset: 0, data: bytes('ab') });
  clock.advance(1000);
  const result = assembler.addFragment({ messageId: 'm', totalLength: 4, offset: 2, data: bytes('cd') });
  assert.strictEqual(result.status, 'accepted', 'old bytes are gone, so the message is not complete');
  assert.strictEqual(assembler.usedBytes, 4);
});

test('memory bound rejects new messages but not in-flight ones', () => {
  const clock = new ManualClock();
  const assembler = makeAssembler(clock, 10);
  assert.strictEqual(assembler.addFragment({ messageId: 'a', totalLength: 8, offset: 0, data: bytes('aaaaaaaa') }).status, 'complete');
  assembler.addFragment({ messageId: 'b', totalLength: 8, offset: 0, data: bytes('bbbb') });
  const denied = assembler.addFragment({ messageId: 'c', totalLength: 8, offset: 0, data: bytes('cccc') });
  assert.strictEqual(denied.status, 'rejected');
  assert.match((denied as { reason: string }).reason, /memory/);
  assert.strictEqual(assembler.addFragment({ messageId: 'b', totalLength: 8, offset: 4, data: bytes('bbbb') }).status, 'complete');
  assert.strictEqual(assembler.usedBytes, 0);
});

test('oversized single message is rejected without corrupting state', () => {
  const clock = new ManualClock();
  const assembler = makeAssembler(clock, 4);
  const result = assembler.addFragment({ messageId: 'big', totalLength: 10, offset: 0, data: bytes('ab') });
  assert.strictEqual(result.status, 'rejected');
  assert.strictEqual(assembler.inFlightCount, 0);
});

test('invalid fragments are rejected', () => {
  const clock = new ManualClock();
  const assembler = makeAssembler(clock);
  const cases: AddResult[] = [
    assembler.addFragment({ messageId: 'm', totalLength: 4, offset: 3, data: bytes('ab') }),
    assembler.addFragment({ messageId: 'm', totalLength: 4, offset: -1, data: bytes('a') }),
    assembler.addFragment({ messageId: 'm', totalLength: 4, offset: 0, data: new Uint8Array(0) }),
    assembler.addFragment({ messageId: 'm', totalLength: -1, offset: 0, data: bytes('a') }),
  ];
  for (const result of cases) {
    assert.strictEqual(result.status, 'rejected');
  }
  assert.strictEqual(assembler.inFlightCount, 0);
});

test('sweep expires timed-out generations and frees memory', () => {
  const clock = new ManualClock();
  const assembler = makeAssembler(clock, 1024, 1000);
  assembler.addFragment({ messageId: 'a', totalLength: 10, offset: 0, data: bytes('aa') });
  assembler.addFragment({ messageId: 'b', totalLength: 5, offset: 0, data: bytes('b') });
  assert.strictEqual(assembler.usedBytes, 15);

  clock.advance(500);
  assert.deepStrictEqual(assembler.sweep(), []);

  clock.advance(500);
  const expired = assembler.sweep().sort();
  assert.deepStrictEqual(expired, ['a', 'b']);
  assert.strictEqual(assembler.usedBytes, 0);
  assert.strictEqual(assembler.inFlightCount, 0);
});

test('completed message id can start a fresh generation without waiting', () => {
  const clock = new ManualClock();
  const assembler = makeAssembler(clock);
  const first = assembler.addFragment({ messageId: 'm', totalLength: 2, offset: 0, data: bytes('hi') });
  const second = assembler.addFragment({ messageId: 'm', totalLength: 2, offset: 0, data: bytes('yo') });
  assert.strictEqual(first.status, 'complete');
  assert.strictEqual(second.status, 'complete');
  assert.notStrictEqual(
    (first as { generation: number }).generation,
    (second as { generation: number }).generation,
  );
});

if (failures > 0) {
  console.error(`${failures} test(s) failed`);
  process.exit(1);
}
console.log('all tests passed');
