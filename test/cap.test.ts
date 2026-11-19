import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  CapNegotiator,
  CapabilityRegistry,
  ConnectionCapState,
  CAP_MESSAGE_TAGS,
  ProtocolError,
  parseLine,
  RegistrationGate,
} from '../index.ts';

function negotiator() {
  return new CapNegotiator(
    new CapabilityRegistry([CAP_MESSAGE_TAGS, 'server-time']),
    new ConnectionCapState(),
  );
}

test('CAP REQ with one unknown capability NAKs the whole list', () => {
  const cap = negotiator();
  const reply = cap.handle(['REQ', `${CAP_MESSAGE_TAGS} nope-cap`]);
  assert.equal(reply?.subcommand, 'NAK');
  assert.deepEqual(reply?.capabilities, [CAP_MESSAGE_TAGS, 'nope-cap']);
  assert.equal(cap.getState().has(CAP_MESSAGE_TAGS), false);
});

test('CAP REQ with only supported capabilities ACKs everything atomically', () => {
  const cap = negotiator();
  const reply = cap.handle(['REQ', `${CAP_MESSAGE_TAGS} server-time`]);
  assert.equal(reply?.subcommand, 'ACK');
  assert.deepEqual(reply?.capabilities, [CAP_MESSAGE_TAGS, 'server-time']);
  assert.equal(cap.getState().has(CAP_MESSAGE_TAGS), true);
});

test('a failed REQ does not leave partial capabilities enabled', () => {
  const cap = negotiator();
  cap.handle(['REQ', CAP_MESSAGE_TAGS]);
  const reply = cap.handle(['REQ', 'server-time imaginary']);
  assert.equal(reply?.subcommand, 'NAK');
  assert.equal(cap.getState().has('server-time'), false);
  assert.equal(cap.getState().has(CAP_MESSAGE_TAGS), true);
});

test('CAP LS 302 suspends registration until CAP END', () => {
  const cap = negotiator();
  const ls = cap.handle(['LS', '302']);
  assert.equal(ls?.subcommand, 'LS');
  assert.equal(cap.getState().registrationSuspended, true);

  cap.handle(['END']);
  assert.equal(cap.getState().registrationSuspended, false);
});

test('PRIVMSG while registration is suspended is refused as unregistered', () => {
  const cap = negotiator();
  cap.handle(['LS', '302']);
  const gate = new RegistrationGate(cap.getState());

  const privmsg = parseLine('PRIVMSG #chan :hi');
  const rejection = gate.check(privmsg);
  assert.equal(rejection?.numeric, 451);

  assert.equal(gate.check(parseLine('NICK alice')), undefined);
  assert.equal(gate.check(parseLine('CAP END')), undefined);

  cap.handle(['END']);
  gate.markRegistered();
  assert.equal(gate.check(privmsg), undefined);
});

test('CAP without subcommand and bad LS versions are protocol errors', () => {
  const cap = negotiator();
  assert.throws(() => cap.handle([]), ProtocolError);
  assert.throws(() => cap.handle(['LS', '999']), ProtocolError);
  assert.throws(() => cap.handle(['WAT']), ProtocolError);
});
