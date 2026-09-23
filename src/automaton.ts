import type { Path } from './path.ts';
import type { Pattern, PatternSeg } from './pattern.ts';

export interface MatchResult {
  /** Matched subscription ids, deduplicated, in first-match order. */
  subs: number[];
  /** Number of distinct automaton states entered during traversal (root included). */
  visited: number;
}

class State {
  readonly literal = new Map<string, State>();
  star: State | null = null;
  /** Subscription ids whose pattern ends exactly at this state. */
  readonly subs = new Set<number>();
  /** Number of live patterns routed through (or ending at) this state. */
  refs = 0;
}

/**
 * Shared matching automaton: all subscription patterns are compiled into one
 * segment trie. Literal edges are shared across subscriptions, so long shared
 * prefixes are traversed once per change path instead of once per subscriber.
 * Each state also has at most one '*' edge for single-segment wildcards.
 */
export class Automaton {
  private readonly root = new State();
  private size = 1;

  /** Number of live states in the trie. */
  get stateCount(): number {
    return this.size;
  }

  add(subId: number, pattern: Pattern): void {
    let node = this.root;
    node.refs++;
    for (const seg of pattern) {
      if (seg.kind === 'deep') break; // trailing '**' adds nothing under prefix semantics
      let next: State;
      if (seg.kind === 'star') {
        if (!node.star) {
          node.star = new State();
          this.size++;
        }
        next = node.star;
      } else {
        let child = node.literal.get(seg.value);
        if (!child) {
          child = new State();
          node.literal.set(seg.value, child);
          this.size++;
        }
        next = child;
      }
      node = next;
      node.refs++;
    }
    node.subs.add(subId);
  }

  remove(subId: number, pattern: Pattern): boolean {
    const stack: Array<{ parent: State; seg: PatternSeg; node: State }> = [];
    let node = this.root;
    for (const seg of pattern) {
      if (seg.kind === 'deep') break;
      const next = seg.kind === 'star' ? node.star : node.literal.get(seg.value);
      if (!next) return false;
      stack.push({ parent: node, seg, node: next });
      node = next;
    }
    if (!node.subs.delete(subId)) return false;

    this.root.refs--;
    for (let i = stack.length - 1; i >= 0; i--) {
      const { parent, seg, node: child } = stack[i];
      child.refs--;
      if (child.refs === 0) {
        if (seg.kind === 'star') parent.star = null;
        else parent.literal.delete((seg as { kind: 'lit'; value: string }).value);
        this.size--;
      }
    }
    return true;
  }

  /**
   * Walk the trie along `path`, following literal and '*' edges in parallel.
   * Subscriptions are collected at every prefix state (ancestor semantics).
   */
  match(path: Path): MatchResult {
    const subs: number[] = [];
    const seen = new Set<number>();
    const collect = (state: State): void => {
      for (const id of state.subs) {
        if (!seen.has(id)) {
          seen.add(id);
          subs.push(id);
        }
      }
    };

    let visited = 1;
    let frontier: State[] = [this.root];
    collect(this.root);
    for (const seg of path) {
      const next = new Set<State>();
      for (const state of frontier) {
        const lit = state.literal.get(seg);
        if (lit) next.add(lit);
        if (state.star) next.add(state.star);
      }
      if (next.size === 0) break;
      visited += next.size;
      for (const state of next) collect(state);
      frontier = [...next];
    }
    return { subs, visited };
  }
}
