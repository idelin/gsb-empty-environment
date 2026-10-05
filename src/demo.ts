import { Endpoint } from './endpoint.ts';
import { Simulator } from './simulator.ts';
import { mulberry32 } from './rng.ts';
import { ConnState, PacketKind, type Packet } from './types.ts';

const sim = new Simulator({ latency: 10 });
sim.verbose = true;

const A = new Endpoint('A', { maxBufferedBytes: 64, rto: 100, maxRetries: 5, rng: mulberry32(7) });
const B = new Endpoint('B', { maxBufferedBytes: 64, rto: 100, maxRetries: 5, rng: mulberry32(9) });
sim.add(A);
sim.add(B);

const established = () => A.state === ConnState.Established && B.state === ConnState.Established;
const appSend = (ep: Endpoint, data: string) => {
  const r = ep.send(data, sim.now);
  for (const p of r.out) sim.emit(p);
};

console.log('=== 1. 正常三次握手（双方确认新代次） ===');
A.boot(1);
B.boot(1);
for (const p of A.connect('B', sim.now)) sim.emit(p);
sim.runUntil(established);
appSend(A, 'hello-from-A1');
sim.settle();
console.log('B delivered:', B.delivered);

const oldData = sim.sentLog.find((p) => p.kind === PacketKind.Data && p.src === 'A') as Packet;

console.log('\n=== 2. A 崩溃重启（序号归零、新代次），B 识别后重新握手 ===');
A.boot(2);
for (const p of A.connect('B', sim.now)) sim.emit(p);
sim.runUntil(established);

console.log('\n=== 3. 注入旧连接残包（旧代次、序号与接收窗口完全重叠） ===');
sim.inject({ ...oldData, seq: B.rcvNext, payload: 'stale-garbage' }, sim.now + 5);
sim.settle();
console.log('B delivered:', B.delivered);

console.log('\n=== 4. B 崩溃重启：A 的旧代次数据触发 RESET，双方确认后重连 ===');
B.boot(2);
appSend(A, 'data-for-dead-B');
sim.runUntil(established);
appSend(A, 'hello-again');
sim.settle();
console.log('B delivered:', B.delivered);
