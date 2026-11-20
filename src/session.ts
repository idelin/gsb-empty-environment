import type { SessionQuotas } from "./types.ts";

/**
 * A single WebTransport CONNECT session bound to exactly one origin.
 *
 * Application state (stream/datagram queues, per-origin stats) lives here
 * and is reachable only through the SessionHandle issued to the requesting
 * origin. The pool never exposes a way to enumerate another origin's
 * sessions.
 */
export class Session {
  readonly id: number;
  readonly origin: string;
  readonly url: string;
  readonly draft: string;
  readonly version: string;
  readonly quotas: SessionQuotas;
  state: "open" | "closed" = "open";

  // ---- private per-origin application state: never shared cross-origin ----
  readonly incomingStreams: number[] = [];
  readonly datagramQueue: Uint8Array[] = [];
  readonly appStats = { datagramsReceived: 0, streamsOpened: 0 };

  constructor(init: {
    id: number;
    origin: string;
    url: string;
    draft: string;
    version: string;
    maxStreams: number;
    maxDatagrams: number;
  }) {
    this.id = init.id;
    this.origin = init.origin;
    this.url = init.url;
    this.draft = init.draft;
    this.version = init.version;
    this.quotas = {
      maxStreams: init.maxStreams,
      maxDatagrams: init.maxDatagrams,
      usedStreams: 0,
      usedDatagrams: 0,
    };
  }

  openStream(streamId: number): boolean {
    if (this.state !== "open") return false;
    if (this.quotas.usedStreams >= this.quotas.maxStreams) return false;
    this.quotas.usedStreams++;
    this.appStats.streamsOpened++;
    this.incomingStreams.push(streamId);
    return true;
  }

  receiveDatagram(payload: Uint8Array): boolean {
    if (this.state !== "open") return false;
    if (this.quotas.usedDatagrams >= this.quotas.maxDatagrams) return false;
    this.quotas.usedDatagrams++;
    this.appStats.datagramsReceived++;
    this.datagramQueue.push(payload);
    return true;
  }

  close(): void {
    this.state = "closed";
    this.datagramQueue.length = 0;
    this.incomingStreams.length = 0;
  }
}

/**
 * Capability-style handle handed to the requesting origin. Holding a handle
 * is the only way to reach session state; the pool issues one per session
 * and never re-issues it to a different origin.
 */
export class SessionHandle {
  readonly session: Session;
  readonly origin: string;
  constructor(session: Session, origin: string) {
    this.session = session;
    this.origin = origin;
  }
}
