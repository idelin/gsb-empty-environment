import { ZERO_DIGEST, utf8 } from '../src/encoding.ts';
import { IngestHost, type HostConfig } from '../src/host.ts';
import { generateKey, type KeyPair } from '../src/keys.ts';
import { makeRecord, type SignedRecord } from '../src/records.ts';

export interface Fixture {
  host: IngestHost;
  config: HostConfig;
  author: string;
  gen0: KeyPair;
  gen1: KeyPair;
  relay: KeyPair;
  mediator: KeyPair;
  witnesses: KeyPair[];
}

export function makeFixture(witnessThreshold = 2): Fixture {
  const gen0 = generateKey();
  const gen1 = generateKey();
  const relay = generateKey();
  const mediator = generateKey();
  const witnesses = [generateKey(), generateKey(), generateKey()];
  const author = 'author-' + Buffer.from(gen0.publicDer.slice(0, 4)).toString('hex');
  const config: HostConfig = {
    authors: [{ id: author, gen0Key: gen0.publicDer }],
    relayKeys: [relay.publicDer],
    mediatorKeys: [mediator.publicDer],
    witnessKeys: witnesses.map((w) => w.publicDer),
    witnessThreshold,
  };
  return { host: new IngestHost(config), config, author, gen0, gen1, relay, mediator, witnesses };
}

export class Chain {
  seq: bigint;
  prev: string;
  readonly author: string;
  readonly kp: KeyPair;
  constructor(author: string, kp: KeyPair, startSeq = 0n, startPrev = ZERO_DIGEST) {
    this.author = author;
    this.kp = kp;
    this.seq = startSeq;
    this.prev = startPrev;
  }
  next(body: string, kp: KeyPair = this.kp, keyGen = 0): SignedRecord {
    const rec = makeRecord(kp, {
      author: this.author,
      keyGen,
      seq: this.seq,
      prev: this.prev,
      body: utf8(body),
    });
    this.seq += 1n;
    this.prev = rec.digest;
    return rec;
  }
  branch(): Chain {
    return new Chain(this.author, this.kp, this.seq, this.prev);
  }
}

export function shuffled<T>(items: T[], seed: number): T[] {
  const out = [...items];
  let state = seed >>> 0 || 1;
  const rand = () => {
    state = (state * 1103515245 + 12345) >>> 0;
    return state / 0x100000000;
  };
  for (let i = out.length - 1; i > 0; i--) {
    const j = Math.floor(rand() * (i + 1));
    [out[i], out[j]] = [out[j], out[i]];
  }
  return out;
}

export function feedAll(host: IngestHost, records: SignedRecord[]): void {
  for (const r of records) {
    const res = host.ingestRecord(r);
    if (!res.accepted) throw new Error(`ingest rejected: ${res.reason}`);
  }
}
