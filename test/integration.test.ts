import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  LineDecoder,
  parseLine,
  CapNegotiator,
  CapabilityRegistry,
  ConnectionCapState,
  CAP_MESSAGE_TAGS,
  RegistrationGate,
  forwardMessage,
  type ForwardTarget,
} from '../index.ts';

test('end to end: injected newline is dropped, CAP gate and tagged relay work', () => {
  // 1. Framing splits on the injected newline; the parser then refuses any
  // line that still carries CR/LF/NUL inside it.
  const decoder = new LineDecoder();
  const framed = decoder.feed(
    '@+vendor/a=pwn\\:pwn :alice PRIVMSG #c :ok\nPRIVMSG #c :forged\n',
  );
  assert.deepEqual(framed.lines, [
    '@+vendor/a=pwn\\:pwn :alice PRIVMSG #c :ok',
    'PRIVMSG #c :forged',
  ]);
  assert.throws(() => parseLine('PRIVMSG #c :forged\r\nPRIVMSG x :y'));

  // 2. CAP LS 302 suspends registration and rejects early PRIVMSG.
  const state = new ConnectionCapState();
  const cap = new CapNegotiator(new CapabilityRegistry([CAP_MESSAGE_TAGS]), state);
  cap.handle(['LS', '302']);
  const gate = new RegistrationGate(state);
  const message = parseLine(framed.lines[0]);
  assert.equal(gate.check(message)?.numeric, 451);

  // 3. After CAP END and registration the same message is relayed.
  cap.handle(['END']);
  gate.markRegistered();
  assert.equal(gate.check(message), undefined);

  const withTags: ForwardTarget = {
    id: 'carol',
    capState: negotiatorAck(),
    send: () => {},
  };
  const withoutTags: ForwardTarget = {
    id: 'bob',
    capState: new ConnectionCapState(),
    send: () => {},
  };
  const results = forwardMessage(message, [withTags, withoutTags]);
  assert.ok(results[0].line.startsWith('@+vendor/a=pwn\\:pwn'));
  assert.ok(!results[1].line.startsWith('@'));
});

function negotiatorAck(): ConnectionCapState {
  const capState = new ConnectionCapState();
  new CapNegotiator(new CapabilityRegistry([CAP_MESSAGE_TAGS]), capState).handle([
    'REQ',
    CAP_MESSAGE_TAGS,
  ]);
  return capState;
}
