import {
  ConnState,
  PacketKind,
  genKey,
  sameGen,
  type Generation,
  type NodeId,
  type Packet,
} from './types.ts';

export interface EndpointOptions {
  maxBufferedBytes: number;
  rto: number;
  maxRetries: number;
  rng: () => number;
}

interface UnackedData {
  seq: number;
  payload: string;
  sentAt: number;
}

interface ControlFlight {
  pkt: Packet;
  sentAt: number;
  retries: number;
}

export class Endpoint {
  readonly id: NodeId;
  state: ConnState = ConnState.Closed;
  readonly delivered: string[] = [];
  readonly log: string[] = [];

  private readonly opts: EndpointOptions;
  private myGen: Generation | null = null;
  private peerGen: Generation | null = null;
  private peerNode: NodeId = '';
  private lastPeerIncarnation = 0;

  private sndIsn = 0;
  private sndNxt = 0;
  private sndUna = 0;
  private rcvNxt = 0;

  private wantConnection = false;
  private buffered: string[] = [];
  private bufferedBytes = 0;
  private unacked: UnackedData[] = [];
  private dataRetries = 0;
  private control: ControlFlight | null = null;

  constructor(id: NodeId, opts: EndpointOptions) {
    this.id = id;
    this.opts = opts;
  }

  get generation(): string {
    return this.myGen === null ? '' : genKey(this.myGen);
  }

  get peerGeneration(): string {
    return this.peerGen === null ? '' : genKey(this.peerGen);
  }

  get myGenObj(): Generation | null {
    return this.myGen;
  }

  get peerGenObj(): Generation | null {
    return this.peerGen;
  }

  get rcvNext(): number {
    return this.rcvNxt;
  }

  boot(incarnation: number): void {
    this.myGen = {
      node: this.id,
      incarnation,
      nonce: Math.floor(this.opts.rng() * 2 ** 31),
    };
    this.state = ConnState.Closed;
    this.peerGen = null;
    this.peerNode = '';
    this.lastPeerIncarnation = 0;
    this.wantConnection = false;
    this.buffered = [];
    this.bufferedBytes = 0;
    this.unacked = [];
    this.dataRetries = 0;
    this.control = null;
    this.delivered.length = 0;
    this.note(`boot gen=${this.generation}`);
  }

  connect(peerId: NodeId, now: number): Packet[] {
    if (this.myGen === null) throw new Error('boot() must be called first');
    if (this.state !== ConnState.Closed) return [];
    this.wantConnection = true;
    this.peerNode = peerId;
    return [this.startHandshake(now)];
  }

  send(data: string, now: number): { accepted: boolean; out: Packet[] } {
    if (this.bufferedBytes + data.length > this.opts.maxBufferedBytes) {
      this.note(`buffer full, reject "${data}"`);
      return { accepted: false, out: [] };
    }
    this.buffered.push(data);
    this.bufferedBytes += data.length;
    const out = this.state === ConnState.Established ? this.flushBuffer(now) : [];
    return { accepted: true, out };
  }

  onPacket(pkt: Packet, now: number): Packet[] {
    if (pkt.kind === PacketKind.Reset) return this.onReset(pkt, now);
    switch (this.state) {
      case ConnState.Closed:
        return this.onPacketClosed(pkt, now);
      case ConnState.SynSent:
        return this.onPacketSynSent(pkt, now);
      case ConnState.SynReceived:
        return this.onPacketSynReceived(pkt, now);
      case ConnState.Established:
        return this.onPacketEstablished(pkt, now);
    }
  }

  nextTimerAt(): number | undefined {
    const times: number[] = [];
    if (this.control !== null) times.push(this.control.sentAt + this.opts.rto);
    if (this.unacked.length > 0) times.push(this.unacked[0].sentAt + this.opts.rto);
    return times.length === 0 ? undefined : Math.min(...times);
  }

  tick(now: number): Packet[] {
    const out: Packet[] = [];
    if (this.control !== null && now >= this.control.sentAt + this.opts.rto) {
      if (this.control.retries >= this.opts.maxRetries) {
        this.note('handshake give up');
        this.control = null;
        this.wantConnection = false;
        this.teardown();
      } else {
        this.control.retries += 1;
        this.control.sentAt = now;
        this.note(`retransmit ${this.control.pkt.kind}`);
        out.push(this.control.pkt);
      }
    }
    if (
      this.state === ConnState.Established &&
      this.unacked.length > 0 &&
      now >= this.unacked[0].sentAt + this.opts.rto
    ) {
      if (this.dataRetries >= this.opts.maxRetries) {
        this.note('data give up');
        this.unacked = [];
      } else {
        this.dataRetries += 1;
        this.note(`retransmit ${this.unacked.length} data packet(s)`);
        for (const u of this.unacked) {
          u.sentAt = now;
          out.push(this.dataPacket(u.seq, u.payload));
        }
      }
    }
    return out;
  }

  private onPacketClosed(pkt: Packet, now: number): Packet[] {
    if (pkt.kind === PacketKind.Syn && pkt.srcGen !== null) {
      const knownNode = pkt.srcGen.node === this.peerNode && this.lastPeerIncarnation > 0;
      if (knownNode && pkt.srcGen.incarnation <= this.lastPeerIncarnation) {
        this.note(`reject stale SYN gen=${genKey(pkt.srcGen)}`);
        return [this.resetFor(pkt)];
      }
      this.peerNode = pkt.src;
      this.adoptPeerGen(pkt.srcGen);
      this.rcvNxt = pkt.seq + 1;
      this.sndIsn = this.freshIsn();
      this.sndNxt = this.sndIsn + 1;
      this.sndUna = this.sndIsn;
      this.state = ConnState.SynReceived;
      const synAck = this.makePacket(PacketKind.SynAck, this.sndIsn);
      this.armControl(synAck, now);
      this.note(`passive open, peer=${genKey(pkt.srcGen)}`);
      return [synAck];
    }
    return pkt.srcGen !== null ? [this.resetFor(pkt)] : [];
  }

  private onPacketSynSent(pkt: Packet, now: number): Packet[] {
    if (
      pkt.kind === PacketKind.SynAck &&
      sameGen(pkt.dstGen, this.myGen) &&
      pkt.ack === this.sndIsn + 1 &&
      pkt.srcGen !== null &&
      this.isFreshPeerGen(pkt.srcGen)
    ) {
      this.adoptPeerGen(pkt.srcGen);
      this.rcvNxt = pkt.seq + 1;
      this.sndUna = pkt.ack;
      this.control = null;
      this.state = ConnState.Established;
      this.note('established (initiator)');
      return [this.makePacket(PacketKind.Ack, this.sndNxt), ...this.flushBuffer(now)];
    }
    if (pkt.kind === PacketKind.Syn && pkt.srcGen !== null && this.isFreshPeerGen(pkt.srcGen)) {
      this.adoptPeerGen(pkt.srcGen);
      this.rcvNxt = pkt.seq + 1;
      this.state = ConnState.SynReceived;
      const synAck = this.makePacket(PacketKind.SynAck, this.sndIsn);
      this.armControl(synAck, now);
      this.note('simultaneous open');
      return [synAck];
    }
    if (pkt.srcGen !== null) {
      this.note(`reject stale ${pkt.kind} in SYN-SENT`);
      return [this.resetFor(pkt)];
    }
    return [];
  }

  private onPacketSynReceived(pkt: Packet, now: number): Packet[] {
    const pairMatch = sameGen(pkt.srcGen, this.peerGen) && sameGen(pkt.dstGen, this.myGen);
    if (pkt.kind === PacketKind.SynAck && pairMatch && pkt.ack === this.sndIsn + 1) {
      this.sndUna = pkt.ack;
      this.control = null;
      this.state = ConnState.Established;
      this.note('established (simultaneous open)');
      return [this.makePacket(PacketKind.Ack, this.sndNxt), ...this.flushBuffer(now)];
    }
    if (
      (pkt.kind === PacketKind.Ack || pkt.kind === PacketKind.Data) &&
      pairMatch &&
      pkt.ack === this.sndIsn + 1
    ) {
      this.sndUna = pkt.ack;
      this.control = null;
      this.state = ConnState.Established;
      this.note('established (responder)');
      const out = pkt.kind === PacketKind.Data ? this.onData(pkt, now) : [];
      out.push(...this.flushBuffer(now));
      return out;
    }
    if (pkt.kind === PacketKind.Syn && pkt.srcGen !== null) {
      if (sameGen(pkt.srcGen, this.peerGen)) {
        const synAck = this.makePacket(PacketKind.SynAck, this.sndIsn);
        this.armControl(synAck, now);
        return [synAck];
      }
      if (this.isFreshPeerGen(pkt.srcGen)) {
        this.note('peer restarted during handshake');
        this.adoptPeerGen(pkt.srcGen);
        this.rcvNxt = pkt.seq + 1;
        const synAck = this.makePacket(PacketKind.SynAck, this.sndIsn);
        this.armControl(synAck, now);
        return [synAck];
      }
      return [this.resetFor(pkt)];
    }
    if (pkt.srcGen !== null) return [this.resetFor(pkt)];
    return [];
  }

  private onPacketEstablished(pkt: Packet, now: number): Packet[] {
    const pairMatch = sameGen(pkt.srcGen, this.peerGen) && sameGen(pkt.dstGen, this.myGen);
    if (pairMatch) {
      if (pkt.kind === PacketKind.Data) return this.onData(pkt, now);
      if (pkt.kind === PacketKind.Ack) {
        this.processAck(pkt.ack);
        return [];
      }
      if (pkt.kind === PacketKind.SynAck && pkt.ack === this.sndIsn + 1) {
        this.note('duplicate SYN-ACK, re-ACK');
        return [this.makePacket(PacketKind.Ack, this.sndNxt)];
      }
      return [];
    }
    if (pkt.kind === PacketKind.Syn && pkt.srcGen !== null && this.isFreshPeerGen(pkt.srcGen)) {
      this.note('peer restarted, re-handshake');
      this.unacked = [];
      this.dataRetries = 0;
      this.adoptPeerGen(pkt.srcGen);
      this.rcvNxt = pkt.seq + 1;
      this.sndIsn = this.freshIsn();
      this.sndNxt = this.sndIsn + 1;
      this.sndUna = this.sndIsn;
      this.state = ConnState.SynReceived;
      const synAck = this.makePacket(PacketKind.SynAck, this.sndIsn);
      this.armControl(synAck, now);
      return [synAck];
    }
    if (pkt.srcGen !== null) {
      this.note(`reject stale ${pkt.kind} gen=${genKey(pkt.srcGen)}`);
      return [this.resetFor(pkt)];
    }
    return [];
  }

  private onReset(pkt: Packet, now: number): Packet[] {
    const echoSrc = pkt.echoSrcGen ?? null;
    const echoDst = pkt.echoDstGen ?? null;
    const echoSeq = pkt.echoSeq ?? -1;
    const pairMatch =
      sameGen(echoSrc, this.myGen) &&
      (this.peerGen === null ? echoDst === null : sameGen(echoDst, this.peerGen));
    const seqValid = echoSeq >= this.sndIsn && echoSeq < this.sndNxt;
    if (this.state !== ConnState.Closed && pairMatch && seqValid) {
      this.note('RESET accepted');
      this.teardown();
      if (this.wantConnection) {
        this.note('reconnect after RESET');
        return [this.startHandshake(now)];
      }
      return [];
    }
    this.note('ignore stale RESET');
    return [];
  }

  private onData(pkt: Packet, _now: number): Packet[] {
    this.processAck(pkt.ack);
    if (pkt.payload.length === 0) return [];
    if (pkt.seq === this.rcvNxt) {
      this.rcvNxt += pkt.payload.length;
      this.delivered.push(pkt.payload);
      this.note(`deliver "${pkt.payload}"`);
    } else {
      this.note(`out-of-window data seq=${pkt.seq} want=${this.rcvNxt}`);
    }
    return [this.makePacket(PacketKind.Ack, this.sndNxt)];
  }

  private processAck(ack: number): void {
    if (ack <= this.sndUna || ack > this.sndNxt) return;
    this.sndUna = ack;
    const remaining = this.unacked.filter((u) => u.seq + u.payload.length > ack);
    if (remaining.length !== this.unacked.length) {
      this.dataRetries = 0;
      this.unacked = remaining;
    }
  }

  private startHandshake(now: number): Packet {
    this.sndIsn = this.freshIsn();
    this.sndNxt = this.sndIsn + 1;
    this.sndUna = this.sndIsn;
    this.state = ConnState.SynSent;
    const syn: Packet = {
      kind: PacketKind.Syn,
      src: this.id,
      dst: this.peerNode,
      srcGen: this.myGen,
      dstGen: null,
      seq: this.sndIsn,
      ack: -1,
      payload: '',
    };
    this.armControl(syn, now);
    this.note('send SYN');
    return syn;
  }

  private makePacket(kind: PacketKind, seq: number): Packet {
    return {
      kind,
      src: this.id,
      dst: this.peerNode,
      srcGen: this.myGen,
      dstGen: this.peerGen,
      seq,
      ack: this.rcvNxt,
      payload: '',
    };
  }

  private dataPacket(seq: number, payload: string): Packet {
    return {
      kind: PacketKind.Data,
      src: this.id,
      dst: this.peerNode,
      srcGen: this.myGen,
      dstGen: this.peerGen,
      seq,
      ack: this.rcvNxt,
      payload,
    };
  }

  private resetFor(pkt: Packet): Packet {
    return {
      kind: PacketKind.Reset,
      src: this.id,
      dst: pkt.src,
      srcGen: this.myGen,
      dstGen: null,
      seq: 0,
      ack: -1,
      payload: '',
      echoSrcGen: pkt.srcGen,
      echoDstGen: pkt.dstGen,
      echoSeq: pkt.seq,
    };
  }

  private flushBuffer(now: number): Packet[] {
    const out: Packet[] = [];
    while (this.buffered.length > 0) {
      const data = this.buffered.shift() as string;
      this.bufferedBytes -= data.length;
      const pkt = this.dataPacket(this.sndNxt, data);
      this.sndNxt += data.length;
      this.unacked.push({ seq: pkt.seq, payload: data, sentAt: now });
      out.push(pkt);
    }
    return out;
  }

  private armControl(pkt: Packet, now: number): void {
    this.control = { pkt, sentAt: now, retries: 0 };
  }

  private adoptPeerGen(gen: Generation): void {
    this.peerGen = gen;
    this.lastPeerIncarnation = Math.max(this.lastPeerIncarnation, gen.incarnation);
  }

  private isFreshPeerGen(gen: Generation): boolean {
    return gen.node === this.peerNode && gen.incarnation > this.lastPeerIncarnation;
  }

  private teardown(): void {
    this.state = ConnState.Closed;
    this.peerGen = null;
    this.control = null;
    this.unacked = [];
    this.dataRetries = 0;
  }

  private freshIsn(): number {
    return 1 + Math.floor(this.opts.rng() * 1_000_000);
  }

  private note(msg: string): void {
    this.log.push(`[${this.id} ${this.state}] ${msg}`);
  }
}
