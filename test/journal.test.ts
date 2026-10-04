import test from 'node:test';
import assert from 'node:assert/strict';
import { appendFileSync, readFileSync, writeFileSync, mkdtempSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { Journal } from '../src/journal.ts';
import { JournalCorruptionError } from '../src/errors.ts';
import { crc32hex } from '../src/crc32.ts';

function tempPath(): string {
  return join(mkdtempSync(join(tmpdir(), 'journal-')), 'test.journal');
}

test('appended records survive a reopen (persistence confirmed at append return)', () => {
  const path = tempPath();
  const journal = Journal.open(path);
  journal.append('intent', 'wf-1', { step: 'a' });
  journal.append('result', 'wf-1', { step: 'a', outcome: 'success' });
  journal.close();

  const reopened = Journal.open(path);
  assert.equal(reopened.records.length, 2);
  assert.deepEqual(
    reopened.records.map((r) => r.type),
    ['intent', 'result'],
  );
  reopened.close();
});

test('torn final record (truncated write) is tolerated and cut back', () => {
  const path = tempPath();
  const journal = Journal.open(path);
  journal.append('intent', 'wf-1', { step: 'a' });
  journal.close();

  // Simulate a crash mid-append: partial bytes, no newline.
  appendFileSync(path, 'ffffffff {"seq":1,"type":"resu');

  const reopened = Journal.open(path);
  assert.equal(reopened.records.length, 1, 'only the durable record replays');
  assert.equal(reopened.records[0].type, 'intent');

  // The torn tail was truncated, so the journal keeps accepting records.
  reopened.append('result', 'wf-1', { step: 'a', outcome: 'success' });
  reopened.close();

  const again = Journal.open(path);
  assert.equal(again.records.length, 2);
  assert.equal(again.records[1].type, 'result');
  again.close();
});

test('a complete but corrupt record is an error, not silent truncation', () => {
  const path = tempPath();
  const journal = Journal.open(path);
  journal.append('intent', 'wf-1', { step: 'a' });
  journal.close();

  // A full line with valid framing but a wrong CRC, properly newline-terminated.
  appendFileSync(path, 'deadbeef {"seq":1,"type":"result","workflowId":"wf-1","data":{}}\n');

  assert.throws(() => Journal.open(path), JournalCorruptionError);
});

test('a complete record with valid CRC but broken JSON is an error', () => {
  const path = tempPath();
  const journal = Journal.open(path);
  journal.append('intent', 'wf-1', { step: 'a' });
  journal.close();

  const body = '{"seq":1,"type":'; // valid-CRC'd garbage
  appendFileSync(path, `${crc32hex(body)} ${body}\n`);

  assert.throws(() => Journal.open(path), JournalCorruptionError);
});

test('corruption in the middle (not the tail) is reported', () => {
  const path = tempPath();
  const journal = Journal.open(path);
  journal.append('intent', 'wf-1', { step: 'a' });
  journal.append('result', 'wf-1', { step: 'a', outcome: 'success' });
  journal.close();

  // Flip bytes inside the first record's body, keeping the newline structure.
  const raw = readFileSync(path, 'utf8');
  writeFileSync(path, raw.replace('"intent"', '"inzent"'));

  assert.throws(() => Journal.open(path), /CRC/);
});

test('seq gaps are rejected', () => {
  const path = tempPath();
  const journal = Journal.open(path);
  journal.append('intent', 'wf-1', { step: 'a' });
  journal.close();

  const body = JSON.stringify({ seq: 7, type: 'result', workflowId: 'wf-1', data: {} });
  appendFileSync(path, `${crc32hex(body)} ${body}\n`);

  assert.throws(() => Journal.open(path), JournalCorruptionError);
});
