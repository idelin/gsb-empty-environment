import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  CapNegotiator,
  CapabilityRegistry,
  ConnectionCapState,
  CAP_MESSAGE_TAGS,
  buildForwardLine,
  forwardMessage,
  parseLine,
  type ForwardTarget,
} from '../index.ts';

function target(id: string, negotiated: boolean): ForwardTarget {
  const capState = new ConnectionCapState();
  if (negotiated) {
    const cap = new CapNegotiator(
      new CapabilityRegistry([CAP_MESSAGE_TAGS]),
      capState,
    );
    const reply = cap.handle(['REQ', CAP_MESSAGE_TAGS]);
    assert.equal(reply?.subcommand, 'ACK');
  }
  const received: string[] = [];
  return {
    id,
    capState,
    send: (line) => received.push(line),
  };
}

const attackLine =
  '@+vendor/label=evil\\:;+vendor/other=ok;server-only=drop :alice!u@h PRIVMSG #chan :hello';

test('recipient without message-tags gets no tag section at all', () => {
  const message = parseLine(attackLine);
  const recipient = target('bob', false);
  const line = buildForwardLine(message, recipient);
  assert.ok(!line.startsWith('@'));
  assert.equal(line, ':alice!u@h PRIVMSG #chan :hello');
});

test('negotiated recipient gets only + client tags, properly escaped', () => {
  const message = parseLine(attackLine);
  const recipient = target('carol', true);
  const line = buildForwardLine(message, recipient);
  assert.equal(
    line,
    '@+vendor/label=evil\\:;+vendor/other=ok :alice!u@h PRIVMSG #chan :hello',
  );
  assert.ok(!line.includes('server-only'));
  // The unescaped value really contains the semicolon without forging a tag.
  assert.equal(parseLine(line).tags?.get('+vendor/label'), 'evil;');
});

test('fan-out filters tags per recipient and delivers the same body', () => {
  const message = parseLine(attackLine);
  const bob = target('bob', false);
  const carol = target('carol', true);
  const results = forwardMessage(message, [bob, carol]);

  assert.equal(results[0].negotiated, false);
  assert.ok(!results[0].line.startsWith('@'));
  assert.equal(results[1].negotiated, true);
  assert.ok(results[1].line.startsWith('@+vendor/'));
  assert.ok(results.every((r) => r.line.endsWith('PRIVMSG #chan :hello')));
});

test('an injected semicolon in a tag value cannot forge a tag for the recipient', () => {
  const message = parseLine('@+vendor/a=x;+vendor/b=y :alice PRIVMSG #c :hi');
  // Replace the value with an injection attempt that itself contains ';'.
  message.tags?.set('+vendor/a', 'x;+forged/zzz=1');
  const recipient = target('carol', true);
  const line = buildForwardLine(message, recipient);
  assert.equal(line, '@+vendor/a=x\\:+forged/zzz=1;+vendor/b=y :alice PRIVMSG #c :hi');
  const reparsed = parseLine(line);
  assert.deepEqual([...reparsed.tags!.keys()], ['+vendor/a', '+vendor/b']);
  assert.equal(reparsed.tags?.get('+vendor/a'), 'x;+forged/zzz=1');
});
