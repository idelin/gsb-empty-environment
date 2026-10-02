import type { Message, MarkerMessage, TransferMessage } from "./messages.ts";
import type { ChannelStateRecord, ProcessSnapshotRecord } from "./snapshot.ts";

interface ChannelRecording {
  from: string;
  /** true while transfers arriving on this channel must be logged */
  recording: boolean;
  inFlight: TransferMessage[];
}

interface SnapshotState {
  snapshotId: string;
  recordedAt: number;
  balance: number;
  channels: ChannelRecording[];
}

/**
 * A distributed process participating in Chandy-Lamport snapshots.
 *
 * The process never pauses its business logic: transfers keep flowing
 * while a snapshot is underway. Snapshot rules implemented here:
 *
 *  - Marker receiving rule: on the FIRST marker of a snapshot, record
 *    local state, send a marker on every outgoing channel, and start
 *    recording all other incoming channels.
 *  - Marker sending rule (initiator): same, triggered locally.
 *  - Channel recording stops when the marker arrives on that channel;
 *    transfers received in between form the channel's in-flight state.
 */
export class Process {
  readonly id: string;
  balance: number;
  private peers: string[];
  private snapshots = new Map<string, SnapshotState>();
  /** Wired up by the simulation: hands an outbound message to the network. */
  send: (msg: Message) => void = () => {
    throw new Error(`process ${this.id} is not connected to a network`);
  };

  constructor(id: string, balance: number, peers: string[]) {
    this.id = id;
    this.balance = balance;
    this.peers = peers;
  }

  /** Business action: move money out. Never blocked by snapshots. */
  sendTransfer(id: number, to: string, amount: number, now: number): TransferMessage {
    if (amount <= 0 || amount > this.balance) {
      throw new Error(`invalid transfer of ${amount} from ${this.id} (balance ${this.balance})`);
    }
    this.balance -= amount;
    const msg: TransferMessage = { kind: "transfer", id, from: this.id, to, amount, sentAt: now };
    this.send(msg);
    return msg;
  }

  /** Initiator entry point: record local state and flood markers. */
  initiateSnapshot(snapshotId: string, now: number): void {
    this.recordLocalState(snapshotId, now, null);
  }

  receive(msg: Message, now: number): void {
    if (msg.kind === "transfer") {
      this.receiveTransfer(msg);
    } else {
      this.receiveMarker(msg, now);
    }
  }

  private receiveTransfer(msg: TransferMessage): void {
    // Log to every snapshot still recording this channel, then settle.
    for (const snap of this.snapshots.values()) {
      const channel = snap.channels.find((c) => c.from === msg.from);
      if (channel && channel.recording) channel.inFlight.push(msg);
    }
    this.balance += msg.amount;
  }

  private receiveMarker(msg: MarkerMessage, now: number): void {
    const existing = this.snapshots.get(msg.snapshotId);
    if (!existing) {
      // First marker: record local state, flood markers, and record all
      // incoming channels except the one the marker arrived on.
      this.recordLocalState(msg.snapshotId, now, msg.from);
      return;
    }
    const channel = existing.channels.find((c) => c.from === msg.from);
    if (!channel) throw new Error(`no channel ${msg.from}->${this.id} for snapshot ${msg.snapshotId}`);
    channel.recording = false;
  }

  private recordLocalState(snapshotId: string, now: number, markerFrom: string | null): void {
    if (this.snapshots.has(snapshotId)) {
      throw new Error(`snapshot ${snapshotId} already recorded on ${this.id}`);
    }
    const channels: ChannelRecording[] = this.peers.map((from) => ({
      from,
      // The channel the first marker arrived on is recorded as empty.
      recording: from !== markerFrom,
      inFlight: [],
    }));
    this.snapshots.set(snapshotId, {
      snapshotId,
      recordedAt: now,
      balance: this.balance,
      channels,
    });
    // Marker sending rule: immediately emit a marker on every outgoing channel.
    for (const to of this.peers) {
      const marker: MarkerMessage = { kind: "marker", snapshotId, from: this.id, to };
      this.send(marker);
    }
  }

  isSnapshotComplete(snapshotId: string): boolean {
    const snap = this.snapshots.get(snapshotId);
    return snap !== undefined && snap.channels.every((c) => !c.recording);
  }

  getSnapshot(snapshotId: string): ProcessSnapshotRecord {
    const snap = this.snapshots.get(snapshotId);
    if (!snap) throw new Error(`no snapshot ${snapshotId} on ${this.id}`);
    const channels: ChannelStateRecord[] = snap.channels.map((c) => ({
      from: c.from,
      inFlight: [...c.inFlight],
    }));
    return {
      processId: this.id,
      snapshotId,
      recordedAt: snap.recordedAt,
      balance: snap.balance,
      channels,
    };
  }
}
