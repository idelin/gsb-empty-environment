import { test } from 'node:test';
import assert from 'node:assert/strict';
import { parseLine, LineDecoder, ProtocolError, serializeMessage } from '../index.ts';

test('parseLine parses tags, prefix, command and trailing parameter', () => {
  const msg = parseLine(
    '@+vendor/a=x\\:y;bbb :nick!u@h PRIVMSG #chan :hello world',
  );
  assert.equal(msg.command, 'PRIVMSG');
  assert.equal(msg.prefix, 'nick!u@h');
  assert.deepEqual(msg.params, ['#chan', 'hello world']);
  assert.equal(msg.tags?.get('+vendor/a'), 'x;y');
  assert.equal(msg.tags?.get('bbb'), true);
});

test('any line containing CR, LF or NUL is rejected outright', () => {
  const attack = 'PRIVMSG #a :hi\nPRIVMSG #a :spoof';
  assert.throws(() => parseLine(attack), ProtocolError);
  assert.throws(() => parseLine('PRIVMSG #a :hi\r'), ProtocolError);
  assert.throws(() => parseLine('PRIVMSG #a :hi\0'), ProtocolError);
});

test('LineDecoder splits on injected newline and parser rejects the bad line', () => {
  const decoder = new LineDecoder();
  const result = decoder.feed(
    'PRIVMSG #a :first\nPRIVMSG #a :evil\nQUIT\n',
  );
  assert.deepEqual(result.lines, ['PRIVMSG #a :first', 'PRIVMSG #a :evil', 'QUIT']);
  // The injected "command" is only a forged line if the server acts on it;
  // any CR/LF/NUL carried inside a single logical line is rejected here.
  assert.throws(() => parseLine('PRIVMSG #a :first\rPRIVMSG #a :evil'), ProtocolError);
  assert.throws(() => parseLine('PRIVMSG #a :evil\0'), ProtocolError);
});

test('LineDecoder strips the CRLF terminator and splits frames', () => {
  const decoder = new LineDecoder();
  const result = decoder.feed('NICK alice\r\nUSER a 0 * :A\r\n');
  assert.deepEqual(result.lines, ['NICK alice', 'USER a 0 * :A']);
  assert.deepEqual(result.rejected, []);
});

test('LineDecoder rejects bare CR and NUL inside buffered lines on flush', () => {
  const decoder = new LineDecoder();
  decoder.feed('NICK al\r\vice');
  assert.deepEqual(decoder.flush().rejected, ['NICK al\r\vice']);

  const decoder2 = new LineDecoder();
  decoder2.feed('NICK a\0b');
  assert.equal(decoder2.flush().rejected.length, 1);
});

test('serializeMessage never emits CR/LF/NUL and escapes tag values', () => {
  const line = serializeMessage({
    tags: new Map([['+vendor/a', 'x;y z\\w']]),
    prefix: 'nick!u@h',
    command: 'PRIVMSG',
    params: ['#chan', 'hello world'],
  });
  assert.ok(!/[\r\n\0]/.test(line));
  assert.equal(
    line,
    '@+vendor/a=x\\:y\\sz\\\\w :nick!u@h PRIVMSG #chan :hello world',
  );
});
