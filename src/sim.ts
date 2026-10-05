// 整数 tick 驱动的确定性模拟器：消息按 deliverTick 投递，
// 支持分区、按规则丢包、节点/中心重启（从账本重放）。
import fs from 'node:fs';
import { Center } from './center.ts';
import { EdgeNode } from './node.ts';
import { Ledger } from './ledger.ts';
import { describeMsg } from './types.ts';
import type { Msg, MsgKind, SpendResult } from './types.ts';

export interface ScenarioOp {
  tick: number;
  op: string;
  node?: string;
  amount?: number;
  requestId?: string;
  cost?: number;
  grantId?: string;
  from?: string;
  to?: string;
  kind?: MsgKind;
  count?: number;
  delay?: number;
}

export interface Scenario {
  ticks: number;
  windowLength: number;
  windowBudget: number;
  nodes: string[];
  ops: ScenarioOp[];
}

interface InFlight {
  id: number;
  deliverTick: number;
  from: string;
  to: string;
  msg: Msg;
}

interface DropRule {
  from?: string;
  to?: string;
  kind?: MsgKind;
  count: number;
}

export class Sim {
  readonly scenario: Scenario;
  readonly dataDir: string;
  readonly transcript: string[] = [];
  tick = 0;
  center: Center;
  nodes = new Map<string, EdgeNode>();
  private queue: InFlight[] = [];
  private msgSeq = 0;
  private partitions = new Set<string>();
  private dropRules: DropRule[] = [];

  constructor(scenario: Scenario, dataDir: string) {
    this.scenario = scenario;
    this.dataDir = dataDir;
    fs.rmSync(dataDir, { recursive: true, force: true });
    fs.mkdirSync(dataDir, { recursive: true });
    this.center = new Center(scenario.windowLength, scenario.windowBudget, new Ledger(dataDir, 'center'));
    for (const id of scenario.nodes) {
      this.nodes.set(id, new EdgeNode(id, scenario.windowLength, new Ledger(dataDir, `node-${id}`)));
    }
  }

  private log(line: string): void {
    this.transcript.push(`[t=${String(this.tick).padStart(4, '0')}] ${line}`);
  }

  private send(from: string, to: string, msg: Msg, delay = 1): void {
    const id = ++this.msgSeq;
    for (const rule of this.dropRules) {
      const match =
        rule.count > 0 &&
        (rule.from === undefined || rule.from === from) &&
        (rule.to === undefined || rule.to === to) &&
        (rule.kind === undefined || rule.kind === msg.kind);
      if (match) {
        rule.count -= 1;
        this.log(`DROP M${id} ${from}->${to} ${describeMsg(msg)} (rule)`);
        return;
      }
    }
    this.queue.push({ id, deliverTick: this.tick + delay, from, to, msg });
  }

  private flushCenter(delay = 1): void {
    for (const msg of this.center.drainOutbox()) {
      if (msg.kind === 'grant') this.send('center', msg.grant.nodeId, msg, delay);
      else this.send('center', msg.nodeId, msg, delay);
    }
  }

  private flushNode(id: string): void {
    for (const msg of this.nodes.get(id)?.drainOutbox() ?? []) {
      this.send(id, 'center', msg);
    }
  }

  private deliver(): void {
    const due = this.queue.filter((m) => m.deliverTick === this.tick).sort((a, b) => a.id - b.id);
    this.queue = this.queue.filter((m) => m.deliverTick !== this.tick);
    for (const m of due) {
      if (this.partitions.has(`${m.from}->${m.to}`)) {
        this.log(`DROP M${m.id} ${m.from}->${m.to} ${describeMsg(m.msg)} (partition)`);
        continue;
      }
      this.log(`DELIVER M${m.id} ${m.from}->${m.to} ${describeMsg(m.msg)}`);
      if (m.msg.kind === 'grant') {
        this.nodes.get(m.to)?.onGrant(m.msg.grant, this.tick);
      } else if (m.msg.kind === 'return') {
        this.center.onReturn(m.msg, this.tick);
        this.flushCenter();
      } else {
        this.nodes.get(m.to)?.onReturnConfirm(m.msg, this.tick);
      }
    }
  }

  private applyOp(op: ScenarioOp): void {
    switch (op.op) {
      case 'issue': {
        const r = this.center.issue(op.node as string, op.amount as number, this.tick);
        if (r.ok && r.grant) {
          this.log(`ISSUE ${op.node} amt=${op.amount} -> ${r.grant.grantId} win=${r.grant.windowId} exp=${r.grant.expiresAtTick}`);
        } else {
          this.log(`ISSUE-REJECTED ${op.node} amt=${op.amount} reason=${r.reason}`);
        }
        this.flushCenter(typeof op.delay === 'number' ? op.delay : 1);
        break;
      }
      case 'spend': {
        const node = this.nodes.get(op.node as string);
        if (!node) break;
        const r: SpendResult = node.spend(op.requestId as string, op.cost as number, this.tick);
        if (r.status === 'accepted') {
          const allocs = r.allocs.map((a) => `${a.grantId}:${a.amount}`).join('+');
          this.log(`SPEND ${op.node} req=${op.requestId} cost=${op.cost} ACCEPTED${r.dedup ? '(dedup)' : ''} allocs=${allocs}`);
        } else if (r.status === 'rejected') {
          this.log(`SPEND ${op.node} req=${op.requestId} cost=${op.cost} REJECTED${r.dedup ? '(dedup)' : ''} reason=${r.reason}`);
        } else {
          this.log(`SPEND ${op.node} req=${op.requestId} cost=${op.cost} CONFLICT original=${r.originalCost}`);
        }
        break;
      }
      case 'return': {
        const node = this.nodes.get(op.node as string);
        if (!node) break;
        const msg = node.returnGrant(op.grantId as string, this.tick);
        this.log(msg ? `RETURN ${op.node} ${msg.grantId} unspent=${msg.unspent} id=${msg.returnId}` : `RETURN ${op.node} ${op.grantId} noop`);
        this.flushNode(op.node as string);
        break;
      }
      case 'returnAll': {
        const node = this.nodes.get(op.node as string);
        if (!node) break;
        const msgs = node.returnAll(this.tick);
        for (const m of msgs) this.log(`RETURN ${op.node} ${m.grantId} unspent=${m.unspent} id=${m.returnId}`);
        if (msgs.length === 0) this.log(`RETURN ${op.node} noop`);
        this.flushNode(op.node as string);
        break;
      }
      case 'restart': {
        if (op.node === 'center') {
          this.center = new Center(this.scenario.windowLength, this.scenario.windowBudget, new Ledger(this.dataDir, 'center'));
          this.log('RESTART center (replayed from ledger)');
        } else {
          const id = op.node as string;
          this.nodes.set(id, new EdgeNode(id, this.scenario.windowLength, new Ledger(this.dataDir, `node-${id}`)));
          this.log(`RESTART ${id} (replayed from ledger)`);
        }
        break;
      }
      case 'partition': {
        this.partitions.add(`${op.from}->${op.to}`);
        this.log(`PARTITION ${op.from}->${op.to}`);
        break;
      }
      case 'heal': {
        if (op.from !== undefined && op.to !== undefined) {
          this.partitions.delete(`${op.from}->${op.to}`);
          this.log(`HEAL ${op.from}->${op.to}`);
        } else {
          this.partitions.clear();
          this.log('HEAL all');
        }
        break;
      }
      case 'drop': {
        const rule: DropRule = { count: op.count ?? 1 };
        if (op.from !== undefined) rule.from = op.from;
        if (op.to !== undefined) rule.to = op.to;
        if (op.kind !== undefined) rule.kind = op.kind;
        this.dropRules.push(rule);
        this.log(`DROP-RULE from=${rule.from ?? '*'} to=${rule.to ?? '*'} kind=${rule.kind ?? '*'} count=${rule.count}`);
        break;
      }
      default:
        this.log(`UNKNOWN-OP ${op.op}`);
    }
  }

  run(): void {
    const opsByTick = new Map<number, ScenarioOp[]>();
    for (const op of this.scenario.ops) {
      const list = opsByTick.get(op.tick) ?? [];
      list.push(op);
      opsByTick.set(op.tick, list);
    }
    for (this.tick = 0; this.tick < this.scenario.ticks; this.tick++) {
      this.deliver();
      for (const op of opsByTick.get(this.tick) ?? []) this.applyOp(op);
    }
  }
}
