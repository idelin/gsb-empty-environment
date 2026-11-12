import assert from "node:assert/strict";
import { test } from "node:test";
import { IrcServer, Session } from "../src/server.ts";
import type { Transport } from "../src/server.ts";
import { parseMessage } from "../src/message.ts";

class FakeTransport implements Transport {
  readonly sent: string[] = [];
  closed = false;
  send(data: string): void {
    this.sent.push(data);
  }
  close(): void {
    this.closed = true;
  }
}

function connect(server: IrcServer): { session: Session; transport: FakeTransport } {
  const transport = new FakeTransport();
  const session = server.addSession(transport);
  return { session, transport };
}

function register(session: Session, nick: string): void {
  session.receive(`NICK ${nick}\r\n`);
  session.receive(`USER ${nick} 0 * :${nick} real\r\n`);
}

function lastCommand(transport: FakeTransport) {
  const line = transport.sent[transport.sent.length - 1];
  assert.ok(line, "expected at least one outbound line");
  return parseMessage(line.replace(/\r\n$/, ""));
}

test("CAP LS 302 suspends registration until CAP END", () => {
  const server = new IrcServer();
  const { session, transport } = connect(server);

  session.receive("CAP LS 302\r\n");
  register(session, "alice");
  // Held open: no 001 welcome yet.
  assert.equal(session.registered, false);
  assert.ok(!transport.sent.some((l) => l.startsWith(":* 001")));

  session.receive("CAP END\r\n");
  assert.equal(session.registered, true);
  assert.ok(transport.sent.some((l) => l.includes(" 001 ")));
});

test("PRIVMSG during the CAP 302 hold is rejected as unregistered", () => {
  const server = new IrcServer();
  const { session, transport } = connect(server);

  session.receive("CAP LS 302\r\n");
  session.receive("PRIVMSG bob :hi\r\n");

  const reply = lastCommand(transport);
  assert.equal(reply.command, "451");
  assert.ok(reply.params.join(" ").includes("not registered"));
  assert.equal(session.registered, false);
});

test("PRIVMSG before any registration is rejected with 451", () => {
  const server = new IrcServer();
  const { session, transport } = connect(server);
  session.receive("PRIVMSG bob :hi\r\n");
  assert.equal(lastCommand(transport).command, "451");
});

test("a REQ with one unknown capability is NAKed and enables nothing", () => {
  const server = new IrcServer();
  const { session, transport } = connect(server);
  session.receive("CAP REQ :message-tags echo-message\r\n");

  const reply = lastCommand(transport);
  assert.equal(reply.command, "CAP");
  assert.equal(reply.params[2], "NAK");
  assert.equal(session.cap.messageTags, false);
});

test("an all-supported REQ is ACKed atomically", () => {
  const server = new IrcServer();
  const { session, transport } = connect(server);
  session.receive("CAP REQ :message-tags\r\n");
  const reply = lastCommand(transport);
  assert.equal(reply.params[2], "ACK");
  assert.equal(session.cap.messageTags, true);
});

test("relays client-only tags only to message-tags recipients", () => {
  const server = new IrcServer();
  const sender = connect(server);
  const negotiated = connect(server);
  const plain = connect(server);

  register(sender.session, "alice");
  register(negotiated.session, "bob");
  register(plain.session, "carol");

  negotiated.session.receive("CAP REQ :message-tags\r\n");
  for (const peer of [sender, negotiated, plain]) {
    peer.session.receive("JOIN #room\r\n");
  }

  // On the wire the value's semicolon and backslash are escaped; the parser
  // unescapes them and the relay must re-escape when writing out.
  sender.session.receive("@+foo=a\\:b\\\\c PRIVMSG #room :hello\r\n");

  const bobLine = negotiated.transport.sent.find((l) => l.includes(" PRIVMSG "));
  assert.ok(bobLine);
  assert.ok(bobLine?.startsWith("@+foo=a\\:b\\\\c "));

  const carolLine = plain.transport.sent.find((l) => l.includes(" PRIVMSG "));
  assert.ok(carolLine);
  // Client-only tag stripped entirely for the non-negotiated recipient.
  assert.ok(!carolLine.startsWith("@"));
  assert.ok(!carolLine.includes("+foo"));
});

test("an unescaped semicolon in a tag value cannot forge a second tag", () => {
  const server = new IrcServer();
  const sender = connect(server);
  const recipient = connect(server);
  register(sender.session, "alice");
  register(recipient.session, "bob");
  recipient.session.receive("CAP REQ :message-tags\r\n");

  // A bare ';' is a tag separator on the wire: it can only create a second
  // tag entry, never break into the command portion. Both stay confined to the
  // tag section and are re-serialized safely.
  sender.session.receive("@+foo=a;forged=1 PRIVMSG bob :hi\r\n");
  const line = recipient.transport.sent.find((l) => l.includes(" PRIVMSG "));
  assert.ok(line);
  assert.ok(line?.startsWith("@+foo=a;forged=1 "));
  assert.equal((line?.match(/ PRIVMSG /g) ?? []).length, 1);
});

test("server tags survive even for a non-negotiated recipient", () => {
  const server = new IrcServer();
  const sender = connect(server);
  const plain = connect(server);
  register(sender.session, "alice");
  register(plain.session, "dave");

  sender.session.receive("@account=alice PRIVMSG dave :hi\r\n");
  const line = plain.transport.sent.find((l) => l.includes(" PRIVMSG "));
  assert.ok(line?.startsWith("@account=alice "));
});

test("an inline newline closes the connection with ERROR instead of parsing", () => {
  const server = new IrcServer();
  const { session, transport } = connect(server);
  session.receive("PRIVMSG bob :first\nFAKECOMMAND\r\n");
  assert.equal(transport.closed, true);
  assert.ok(transport.sent.some((l) => l.includes(" ERROR ")));
  // The injected command was never dispatched.
  assert.ok(!transport.sent.some((l) => l.includes("FAKECOMMAND")));
});

test("a NUL in the line closes the connection", () => {
  const server = new IrcServer();
  const { session, transport } = connect(server);
  session.receive("NICK al\0ice\r\n");
  assert.equal(transport.closed, true);
});

test("channel PRIVMSG relays to joined members with recipient tag filtering", () => {
  const server = new IrcServer();
  const sender = connect(server);
  const tagged = connect(server);
  const plain = connect(server);
  register(sender.session, "alice");
  register(tagged.session, "bob");
  register(plain.session, "carol");

  tagged.session.receive("CAP REQ :message-tags\r\n");
  for (const peer of [sender, tagged, plain]) {
    peer.session.receive("JOIN #room\r\n");
  }

  sender.session.receive("@+x=1 PRIVMSG #room :chat\r\n");

  const bobMsg = tagged.transport.sent.find((l) => l.includes("PRIVMSG #room"));
  assert.ok(bobMsg?.startsWith("@+x=1 "));
  const carolMsg = plain.transport.sent.find((l) => l.includes("PRIVMSG #room"));
  assert.ok(carolMsg && !carolMsg.startsWith("@"));
});
