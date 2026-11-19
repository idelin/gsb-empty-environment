import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  escapeTagValue,
  unescapeTagValue,
  parseTagString,
  serializeTags,
  selectClientTags,
} from '../index.ts';
import { ProtocolError, SerializationError } from '../index.ts';

test('escapeTagValue encodes ;, space, backslash, CR and LF', () => {
  assert.equal(escapeTagValue('a;b c\\d\re\nf'), 'a\\:b\\sc\\\\d\\re\\nf');
});

test('escape then unescape round-trips an injection payload', () => {
  const payload = 'pwn;+evil.com/x=1 pwn\\two\r\nPRIVMSG Nick :hi';
  assert.equal(unescapeTagValue(escapeTagValue(payload)), payload);
});

test('escaped semicolon cannot create a second tag on the wire', () => {
  const wire = serializeTags(new Map([['+vendor/a', 'x;+vendor/b=y']]));
  assert.equal(wire, '+vendor/a=x\\:+vendor/b=y');
  const reparsed = parseTagString(wire);
  assert.deepEqual([...reparsed.keys()], ['+vendor/a']);
  assert.equal(reparsed.get('+vendor/a'), 'x;+vendor/b=y');
});

test('escaped backslash and space stay inside the value', () => {
  const wire = serializeTags(new Map([['+v/k', 'a\\b c']]));
  assert.equal(wire, '+v/k=a\\\\b\\sc');
  assert.equal(parseTagString(wire).get('+v/k'), 'a\\b c');
});

test('NUL in a tag value is refused instead of written', () => {
  assert.throws(() => escapeTagValue('a\0b'), SerializationError);
});

test('parseTagString rejects empty entries and bad names', () => {
  assert.throws(() => parseTagString('a;;b'), ProtocolError);
  assert.throws(() => parseTagString('bad name=v'), ProtocolError);
  assert.throws(() => parseTagString('@foo=v'), ProtocolError);
  assert.throws(() => parseTagString(''), ProtocolError);
});

test('selectClientTags keeps only + prefixed tags', () => {
  const tags = parseTagString('aaa=1;+vendor/client=2;+x;server-tag=3');
  const clientTags = selectClientTags(tags);
  assert.deepEqual([...clientTags.keys()], ['+vendor/client', '+x']);
});
