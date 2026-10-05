import { Endpoint } from '../src/endpoint.ts';
import { Simulator, type NetworkHooks } from '../src/simulator.ts';
import { mulberry32 } from '../src/rng.ts';
import { ConnState } from '../src/types.ts';

export interface Pair {
  sim: Simulator;
  a: Endpoint;
  b: Endpoint;
}

export function makePair(opts: { maxBufferedBytes?: number; hooks?: NetworkHooks } = {}): Pair {
  const sim = new Simulator({ latency: 10, hooks: opts.hooks });
  const buf = opts.maxBufferedBytes ?? 4096;
  const a = new Endpoint('A', { maxBufferedBytes: buf, rto: 100, maxRetries: 5, rng: mulberry32(11) });
  const b = new Endpoint('B', { maxBufferedBytes: buf, rto: 100, maxRetries: 5, rng: mulberry32(22) });
  sim.add(a);
  sim.add(b);
  return { sim, a, b };
}

export function bootAndConnect(p: Pair, incA = 1, incB = 1): void {
  p.a.boot(incA);
  p.b.boot(incB);
  for (const pkt of p.a.connect('B', p.sim.now)) p.sim.emit(pkt);
}

export function waitEstablished(p: Pair): void {
  p.sim.runUntil(() => p.a.state === ConnState.Established && p.b.state === ConnState.Established);
}

export function send(p: Pair, from: Endpoint, data: string): boolean {
  const r = from.send(data, p.sim.now);
  for (const pkt of r.out) p.sim.emit(pkt);
  return r.accepted;
}
