import { mulberry32 } from "./rng";
import { ReplayHarness, ReplaySummary, Step } from "./types";
import { validateScript } from "./validate";

export interface SimulatorOptions {
  nodes: string[];
  seed: number;
  tickMs?: number;
  electionTimeoutBaseMs?: number;
  electionTimeoutStepMs?: number;
  recoveryMs?: number;
  maxSimMs?: number;
}

interface NodeState {
  id: string;
  up: boolean;
  term: number;
  isLeader: boolean;
  lastHeardMs: number;
  recoverAtMs: number;
}

/**
 * Deterministic, seed-driven simulated cluster used as the default replay
 * harness. Models a Raft-ish protocol: leaders heartbeat every tick,
 * followers that stop hearing from a leader start an election, and a node
 * isolated by packet loss keeps believing it is leader. The checked
 * assertion is "at most one concurrent leader" (no split-brain).
 *
 * A real deployment would replace this with an adapter that replays the
 * script against the actual system under test; the reducer only depends
 * on the ReplayHarness interface.
 */
export class SimulatedCluster implements ReplayHarness {
  private readonly nodes: string[];
  private readonly seed: number;
  private readonly tickMs: number;
  private readonly electionTimeoutBaseMs: number;
  private readonly electionTimeoutStepMs: number;
  private readonly recoveryMs: number;
  private readonly maxSimMs: number;

  constructor(opts: SimulatorOptions) {
    this.nodes = opts.nodes.slice();
    this.seed = opts.seed;
    this.tickMs = opts.tickMs ?? 10;
    this.electionTimeoutBaseMs = opts.electionTimeoutBaseMs ?? 100;
    this.electionTimeoutStepMs = opts.electionTimeoutStepMs ?? 30;
    this.recoveryMs = opts.recoveryMs ?? 150;
    this.maxSimMs = opts.maxSimMs ?? 600_000;
  }

  replay(steps: Step[]): ReplaySummary {
    const validation = validateScript(steps, {
      nodes: this.nodes,
      recoveryMs: this.recoveryMs,
    });
    if (!validation.ok) {
      return {
        status: "invalid",
        invalidReason: validation.errors.join("; "),
        simulatedMs: 0,
        events: 0,
        finalLeaders: [],
        finalTerms: {},
      };
    }

    const rng = mulberry32(this.seed);
    const loss = new Map<string, number>(this.nodes.map((id) => [id, 0]));
    const states = new Map<string, NodeState>(
      this.nodes.map((id) => [
        id,
        { id, up: true, term: 0, isLeader: false, lastHeardMs: 0, recoverAtMs: 0 },
      ])
    );
    let now = 0;
    let events = 0;

    const timeoutOf = (id: string): number =>
      this.electionTimeoutBaseMs + this.nodes.indexOf(id) * this.electionTimeoutStepMs;

    const deliver = (from: string, to: string): boolean =>
      rng() >= (loss.get(from) ?? 0) && rng() >= (loss.get(to) ?? 0);

    const leadersUp = (): NodeState[] =>
      [...states.values()].filter((s) => s.up && s.isLeader);

    const snapshot = (status: ReplaySummary["status"], extra: Partial<ReplaySummary>): ReplaySummary => ({
      status,
      simulatedMs: now,
      events,
      finalLeaders: leadersUp().map((l) => l.id),
      finalTerms: Object.fromEntries([...states.values()].map((s) => [s.id, s.term])),
      ...extra,
    });

    /** Advance one tick; returns the violated assertion, if any. */
    const tick = (): string | null => {
      now += this.tickMs;

      for (const s of states.values()) {
        if (!s.up && s.recoverAtMs <= now) {
          s.up = true;
          s.isLeader = false;
          s.lastHeardMs = now;
          events++;
        }
      }

      for (const leader of states.values()) {
        if (!leader.up || !leader.isLeader) continue;
        for (const f of states.values()) {
          if (!f.up || f.id === leader.id) continue;
          if (!deliver(leader.id, f.id)) continue;
          if (leader.term > f.term) f.term = leader.term;
          if (leader.term >= f.term) {
            f.isLeader = false;
            f.lastHeardMs = now;
          }
        }
      }

      for (const s of states.values()) {
        if (!s.up || s.isLeader) continue;
        if (now - s.lastHeardMs < timeoutOf(s.id)) continue;
        s.term += 1;
        let votes = 1;
        for (const m of states.values()) {
          if (!m.up || m.id === s.id) continue;
          if (m.term > s.term) continue;
          if (!deliver(s.id, m.id) || !deliver(m.id, s.id)) continue;
          votes += 1;
          m.term = s.term;
          m.isLeader = false;
          m.lastHeardMs = now;
        }
        if (votes > states.size / 2) s.isLeader = true;
        s.lastHeardMs = now;
        events++;
      }

      const leaders = leadersUp();
      if (leaders.length > 1) {
        return (
          "split-brain: concurrent leaders " +
          leaders.map((l) => `${l.id}@term${l.term}`).join(", ")
        );
      }
      return null;
    };

    for (const step of steps) {
      if (step.type === "delay") {
        let remaining = step.ms;
        while (remaining >= this.tickMs) {
          remaining -= this.tickMs;
          const violation = tick();
          if (violation) {
            return snapshot("fail", { assertion: violation, violatedAtMs: now });
          }
        }
        now += remaining;
      } else if (step.type === "packet-loss") {
        loss.set(step.node, step.rate);
        events++;
      } else if (step.type === "restart") {
        const s = states.get(step.node);
        if (s) {
          s.up = false;
          s.isLeader = false;
          s.recoverAtMs = now + this.recoveryMs;
          events++;
        }
      }
      if (now > this.maxSimMs) {
        return snapshot("pass", { invalidReason: undefined });
      }
    }

    return snapshot("pass", {});
  }
}
