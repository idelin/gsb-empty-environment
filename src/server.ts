import { CapNegotiation, splitCapabilityList, SUPPORTED_CAPABILITIES } from "./cap.ts";
import { ProtocolError } from "./errors.ts";
import { LineFramer } from "./lineFramer.ts";
import { buildMessage, parseMessage, serializeMessage } from "./message.ts";
import type { Message } from "./message.ts";
import { tagsForRecipient } from "./tags.ts";
import type { TagMap } from "./tags.ts";

/** Pluggable transport so the protocol core is testable without real sockets. */
export interface Transport {
  send(data: string): void;
  close(): void;
}

const SERVER_NAME = "irc.example.dev";

/**
 * One client connection. Owns its line framer, CAP state and registration
 * state and talks to the shared {@link IrcServer} for relaying.
 */
export class Session {
  readonly framer: LineFramer;
  readonly cap = new CapNegotiation();

 nick: string | null = null;
  username: string | null = null;
  realname: string | null = null;
  registered = false;
  closed = false;

  /** Last PING token, used to answer a client PING. */
  private pingToken: string | null = null;

  readonly id: string;
  private readonly transport: Transport;
  private readonly server: IrcServer;

  constructor(id: string, transport: Transport, server: IrcServer) {
    this.id = id;
    this.transport = transport;
    this.server = server;
    this.framer = new LineFramer((line) => this.handleLine(line));
  }

  /** Feed raw bytes/strings from the transport into the framer. */
  receive(chunk: Buffer | string): void {
    if (this.closed) {
      return;
    }
    try {
      this.framer.feed(chunk);
    } catch (error) {
      this.fail(error instanceof Error ? error.message : "framing error");
    }
  }

  /** Send an already-built message, appending CRLF. */
  send(message: Message): void {
    if (this.closed) {
      return;
    }
    this.transport.send(`${serializeMessage(message)}\r\n`);
  }

  close(): void {
    if (this.closed) {
      return;
    }
    this.closed = true;
    this.transport.close();
    this.server.removeSession(this);
  }

  private fail(reason: string): void {
    this.send(buildMessage(SERVER_NAME, "ERROR", [`Closing link: ${reason}`]));
    this.close();
  }

  private handleLine(line: string): void {
    let message: Message;
    try {
      message = parseMessage(line);
    } catch (error) {
      this.fail(error instanceof Error ? error.message : "parse error");
      return;
    }

    // Clients never send a prefix. A leading ':' here is a spoofing attempt.
    if (message.prefix !== null) {
      this.fail("client messages must not contain a prefix");
      return;
    }

    try {
      this.dispatch(message);
    } catch (error) {
      if (error instanceof ProtocolError) {
        this.fail(error.message);
        return;
      }
      throw error;
    }
  }

  private dispatch(message: Message): void {
    if (!this.registered) {
      this.dispatchPreRegistration(message);
      return;
    }
    this.dispatchRegistered(message);
  }

  private dispatchPreRegistration(message: Message): void {
    switch (message.command) {
      case "CAP":
        this.handleCap(message);
        return;
      case "NICK":
        this.handleNick(message);
        break;
      case "USER":
        this.handleUser(message);
        break;
      case "QUIT":
        this.close();
        return;
      case "PING":
        this.handlePing(message);
        return;
      default:
        // While unregistered (including the CAP 302 hold) every other command,
        // PRIVMSG included, is rejected as ERR_NOTREGISTERED.
        this.numeric("451", [message.command, "You have not registered"]);
        return;
    }
    this.tryCompleteRegistration();
  }

  private dispatchRegistered(message: Message): void {
    switch (message.command) {
      case "CAP":
        this.handleCap(message);
        return;
      case "PING":
        this.handlePing(message);
        return;
      case "QUIT":
        this.close();
        return;
      case "PRIVMSG":
        this.handlePrivmsg(message);
        return;
      case "JOIN":
        this.handleJoin(message);
        return;
      default:
        this.numeric("421", [message.command, "Unknown command"]);
    }
  }

  private handleCap(message: Message): void {
    const sub = (message.params[0] ?? "").toUpperCase();
    switch (sub) {
      case "LS": {
        const version = Number.parseInt(message.params[1] ?? "300", 10) || 300;
        this.cap.handleLs(version);
        const advertised = [...SUPPORTED_CAPABILITIES].join(" ");
        this.numeric("CAP", [this.nick ?? "*", "LS", advertised]);
        return;
      }
      case "REQ": {
        if (message.params[1] === undefined) {
          throw new ProtocolError("CAP REQ requires a capability list");
        }
        const requested = splitCapabilityList(message.params[1]);
        const result = this.cap.handleReq(requested);
        this.numeric("CAP", [
          this.nick ?? "*",
          result.acknowledged ? "ACK" : "NAK",
          result.capabilities.join(" "),
        ]);
        return;
      }
      case "END":
        this.cap.handleEnd();
        this.tryCompleteRegistration();
        return;
      case "LIST":
        this.numeric("CAP", [this.nick ?? "*", "LIST", [...this.cap.enabled].join(" ")]);
        return;
      default:
        throw new ProtocolError(`unsupported CAP subcommand: ${sub}`);
    }
  }

  private handleNick(message: Message): void {
    const nick = message.params[0];
    if (nick === undefined || nick.length === 0) {
      this.numeric("431", ["No nickname given"]);
      return;
    }
    if (!/^[A-Za-z_[\]\\`^{|}][A-Za-z0-9_[\]\\`^{|}-]*$/.test(nick)) {
      this.numeric("432", [nick, "Erroneous nickname"]);
      return;
    }
    if (this.server.isNickInUse(nick, this)) {
      this.numeric("433", [nick, "Nickname is already in use"]);
      return;
    }
    this.nick = nick;
  }

  private handleUser(message: Message): void {
    const username = message.params[0];
    const realname = message.params[3] ?? message.params[0];
    if (username === undefined || username.length === 0) {
      throw new ProtocolError("USER requires a username");
    }
    this.username = username;
    this.realname = realname;
  }

  private handlePing(message: Message): void {
    this.pingToken = message.params[0] ?? "";
    this.send(buildMessage(SERVER_NAME, "PONG", [SERVER_NAME, this.pingToken]));
  }

  private handlePrivmsg(message: Message): void {
    const target = message.params[0];
    const text = message.params[1];
    if (target === undefined || text === undefined) {
      this.numeric("461", ["PRIVMSG", "Not enough parameters"]);
      return;
    }
    if (target.startsWith("#") || target.startsWith("&")) {
      this.server.relayToChannel(this, target, message.tags, text);
    } else {
      this.server.relayToNick(this, target, message.tags, text);
    }
  }

  private handleJoin(message: Message): void {
    const channel = message.params[0];
    if (channel === undefined || channel.length === 0) {
      this.numeric("461", ["JOIN", "Not enough parameters"]);
      return;
    }
    this.server.join(this, channel);
  }

  private tryCompleteRegistration(): void {
    if (this.registered || this.cap.registrationHeld) {
      return;
    }
    if (this.nick === null || this.username === null) {
      return;
    }
    this.registered = true;
    this.server.onRegistered(this);
  }

  /** Send a server-sourced numeric or CAP line addressed to this session. */
  numeric(command: string, params: string[]): void {
    this.send(buildMessage(SERVER_NAME, command, [this.nick ?? "*", ...params]));
  }

  /**
   * Deliver a relayed PRIVMSG, filtering the attached tags according to this
   * recipient's negotiated capabilities.
   */
  deliverRelay(sender: Session, target: string, text: string, tags: TagMap): void {
    const visibleTags = tagsForRecipient(tags, this.cap.messageTags);
    const prefix = sender.mask();
    this.send(buildMessage(prefix, "PRIVMSG", [target, text], visibleTags));
  }

  mask(): string {
    return `${this.nick ?? "*"}!${this.username ?? "user"}@${SERVER_NAME}`;
  }
}

/**
 * Shared server state: session/channel registry and the relay rules.
 */
export class IrcServer {
  private readonly sessions = new Map<string, Session>();
  private readonly channels = new Map<string, Set<Session>>();
  private nextId = 1;

  addSession(transport: Transport): Session {
    const session = new Session(`s${this.nextId++}`, transport, this);
    this.sessions.set(session.id, session);
    return session;
  }

  removeSession(session: Session): void {
    this.sessions.delete(session.id);
    for (const members of this.channels.values()) {
      members.delete(session);
    }
  }

  isNickInUse(nick: string, except: Session): boolean {
    for (const session of this.sessions.values()) {
      if (session !== except && session.registered && session.nick === nick) {
        return true;
      }
    }
    return false;
  }

  onRegistered(session: Session): void {
    session.numeric("001", [`Welcome to the Internet Relay Network ${session.mask()}`]);
  }

  join(session: Session, channel: string): void {
    let members = this.channels.get(channel);
    if (members === undefined) {
      members = new Set();
      this.channels.set(channel, members);
    }
    members.add(session);
    for (const member of members) {
      member.send(buildMessage(session.mask(), "JOIN", [channel]));
    }
  }

  relayToChannel(sender: Session, channel: string, tags: TagMap, text: string): void {
    const members = this.channels.get(channel);
    if (members === undefined || !members.has(sender)) {
      sender.numeric("404", [channel, "Cannot send to channel"]);
      return;
    }
    for (const member of members) {
      if (member !== sender) {
        member.deliverRelay(sender, channel, text, tags);
      }
    }
  }

  relayToNick(sender: Session, targetNick: string, tags: TagMap, text: string): void {
    const target = this.findByNick(targetNick);
    if (target === undefined) {
      sender.numeric("401", [targetNick, "No such nick/channel"]);
      return;
    }
    target.deliverRelay(sender, targetNick, text, tags);
  }

  private findByNick(nick: string): Session | undefined {
    for (const session of this.sessions.values()) {
      if (session.registered && session.nick === nick) {
        return session;
      }
    }
    return undefined;
  }
}
