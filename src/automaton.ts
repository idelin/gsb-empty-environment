import type { Pattern } from './pattern.ts';

interface State {
  id: number;
  literal: Map<string, State>;
  star: State | null;     // '*'  : consumes exactly one segment
  starStar: State | null; // '**' : epsilon edge into a self-looping state
  loop: boolean;          // self-loop on any segment (target of a '**' edge)
  subs: Set<number>;      // subscription ids whose pattern ends here
}

export interface MatchResult {
  subs: number[];  // matching subscription ids, sorted, de-duplicated
  visited: number; // number of automaton state activations processed
}

/**
 * All subscription patterns compiled into ONE shared NFA: literal edges,
 * single-segment wildcard edges, and '**' compiled to an epsilon edge into a
 * self-looping state. Patterns with common prefixes share states, so matching
 * a path traverses each shared state once regardless of subscriber count.
 *
 * Subtree semantics: a subscription matches when any active state carrying its
 * id is reached at ANY position along the path (pattern matches a prefix).
 */
export class Automaton {
  private root: State;
  private count = 0;

  constructor() {
    this.root = this.newState();
  }

  get stateCount(): number {
    return this.count;
  }

  private newState(): State {
    return { id: this.count++, literal: new Map(), star: null, starStar: null, loop: false, subs: new Set() };
  }

  add(subId: number, pattern: Pattern): void {
    let s = this.root;
    for (const seg of pattern) {
      if (seg === '*') {
        if (!s.star) s.star = this.newState();
        s = s.star;
      } else if (seg === '**') {
        if (!s.starStar) {
          s.starStar = this.newState();
          s.starStar.loop = true;
        }
        s = s.starStar;
      } else {
        let next = s.literal.get(seg);
        if (!next) {
          next = this.newState();
          s.literal.set(seg, next);
        }
        s = next;
      }
    }
    s.subs.add(subId);
  }

  match(path: readonly string[]): MatchResult {
    const matched = new Set<number>();
    let visited = 0;
    let active = this.closure([this.root]);
    for (let pos = 0; pos <= path.length; pos++) {
      const next = new Map<number, State>();
      for (const s of active) {
        visited++;
        for (const id of s.subs) matched.add(id);
        if (pos === path.length) continue;
        const seg = path[pos];
        const lit = s.literal.get(seg);
        if (lit) this.addClosed(next, lit);
        if (s.star) this.addClosed(next, s.star);
        if (s.loop) this.addClosed(next, s);
      }
      active = [...next.values()];
    }
    return { subs: [...matched].sort((a, b) => a - b), visited };
  }

  /** Epsilon closure: '**' edges consume zero segments, so their targets are active too. */
  private closure(states: State[]): State[] {
    const map = new Map<number, State>();
    for (const s of states) this.addClosed(map, s);
    return [...map.values()];
  }

  private addClosed(map: Map<number, State>, s: State): void {
    if (map.has(s.id)) return;
    map.set(s.id, s);
    if (s.starStar) this.addClosed(map, s.starStar);
  }
}
