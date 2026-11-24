import type { NormalizedEvent, NormalizedGraph } from "./model.ts";
import type { EventId } from "./types.ts";

class UnionFind {
  private readonly parent: number[];

  constructor(size: number) {
    this.parent = Array.from({ length: size }, (_, index) => index);
  }

  find(index: number): number {
    let root = index;
    while (this.parent[root] !== root) {
      root = this.parent[root] as number;
    }
    let cursor = index;
    while (this.parent[cursor] !== cursor) {
      const next = this.parent[cursor] as number;
      this.parent[cursor] = root;
      cursor = next;
    }
    return root;
  }

  union(left: number, right: number): void {
    const leftRoot = this.find(left);
    const rightRoot = this.find(right);
    if (leftRoot !== rightRoot) {
      this.parent[rightRoot] = leftRoot;
    }
  }
}

interface TokenUnion {
  representative(token: string): string;
}

function buildTokenUnion(graph: NormalizedGraph): TokenUnion {
  const indexOf = new Map<string, number>();
  const entries: string[] = [];
  const remember = (token: string): number => {
    const existing = indexOf.get(token);
    if (existing !== undefined) {
      return existing;
    }
    const next = entries.length;
    entries.push(token);
    indexOf.set(token, next);
    return next;
  };
  const pathToken = (path: string): string => `p:${path}`;
  const handleToken = (handle: string): string => `h:${handle}`;

  for (const event of graph.events) {
    const op = event.op;
    switch (op.kind) {
      case "create":
      case "restore":
      case "replace":
      case "open":
      case "readPath":
      case "unlink":
        remember(pathToken(op.path));
        break;
      case "read":
        remember(handleToken(op.handle));
        if (op.path !== undefined) {
          remember(pathToken(op.path));
        }
        break;
      case "close":
        remember(handleToken(op.handle));
        break;
      case "rename":
      case "link":
        remember(pathToken(op.from));
        remember(pathToken(op.to));
        break;
      case "copy":
        remember(handleToken(op.handle));
        remember(pathToken(op.to));
        break;
      case "setClass":
        if (op.path !== undefined) {
          remember(pathToken(op.path));
        }
        if (op.handle !== undefined) {
          remember(handleToken(op.handle));
        }
        break;
      default:
        break;
    }
  }
  for (const entry of graph.initial) {
    remember(pathToken(entry.path));
  }

  const union = new UnionFind(entries.length);
  for (const event of graph.events) {
    switch (event.op.kind) {
      case "rename":
      case "link": {
        const from = remember(pathToken(event.op.from));
        const to = remember(pathToken(event.op.to));
        union.union(from, to);
        break;
      }
      case "open": {
        const path = remember(pathToken(event.op.path));
        const handle = remember(handleToken(event.op.handle));
        union.union(path, handle);
        break;
      }
      case "setClass":
        if (event.op.path !== undefined && event.op.handle !== undefined) {
          const path = remember(pathToken(event.op.path));
          const handle = remember(handleToken(event.op.handle));
          union.union(path, handle);
        }
        break;
      default:
        break;
    }
  }

  return {
    representative(token: string): string {
      const index = indexOf.get(token);
      if (index === undefined) {
        return token;
      }
      return `#${union.find(index)}`;
    },
  };
}

function lockDependent(event: NormalizedEvent): boolean {
  switch (event.op.kind) {
    case "open":
    case "read":
    case "readPath":
    case "copy":
      return true;
    default:
      return false;
  }
}

function isLock(event: NormalizedEvent): boolean {
  return event.op.kind === "lock" || event.op.kind === "unlock";
}

function tokens(
  event: NormalizedEvent,
  tokenUnion: TokenUnion,
): Set<string> {
  const result = new Set<string>();
  const addPath = (path: string): void => {
    result.add(tokenUnion.representative(`p:${path}`));
  };
  const addHandle = (handle: string): void => {
    result.add(tokenUnion.representative(`h:${handle}`));
  };
  switch (event.op.kind) {
    case "create":
    case "restore":
    case "replace":
    case "readPath":
    case "unlink":
      addPath(event.op.path);
      break;
    case "open":
      addPath(event.op.path);
      addHandle(event.op.handle);
      break;
    case "close":
      addHandle(event.op.handle);
      break;
    case "read":
      addHandle(event.op.handle);
      if (event.op.path !== undefined) {
        addPath(event.op.path);
      }
      break;
    case "rename":
    case "link":
      addPath(event.op.from);
      addPath(event.op.to);
      break;
    case "setClass":
      if (event.op.path !== undefined) {
        addPath(event.op.path);
      }
      if (event.op.handle !== undefined) {
        addHandle(event.op.handle);
      }
      break;
    case "copy":
      addHandle(event.op.handle);
      addPath(event.op.to);
      break;
    case "lock":
    case "unlock":
      break;
  }
  return result;
}

function eventsConflict(
  left: NormalizedEvent,
  right: NormalizedEvent,
  leftTokens: ReadonlySet<string>,
  rightTokens: ReadonlySet<string>,
): boolean {
  if (isLock(left) || isLock(right)) {
    if (isLock(left) && isLock(right)) {
      return true;
    }
    return lockDependent(left) || lockDependent(right);
  }
  for (const token of leftTokens) {
    if (rightTokens.has(token)) {
      return true;
    }
  }
  return false;
}

export interface ComponentSpec {
  readonly id: number;
  readonly events: readonly NormalizedEvent[];
  readonly eventIds: readonly EventId[];
}

/**
 * Partition the event graph into independent components.  Two events are in
 * the same component when a partial-order edge connects them or when their
 * resource footprints may overlap.  Components can therefore be verified
 * separately and composed.
 */
export function buildComponents(graph: NormalizedGraph): ComponentSpec[] {
  const count = graph.events.length;
  const union = new UnionFind(count);
  const indexById = new Map<EventId, number>();
  graph.events.forEach((event, index) => indexById.set(event.id, index));

  for (const event of graph.events) {
    const index = indexById.get(event.id) as number;
    for (const dependency of event.before) {
      union.union(index, indexById.get(dependency) as number);
    }
  }

  const tokenUnion = buildTokenUnion(graph);
  const tokenSets = graph.events.map((event) => tokens(event, tokenUnion));
  for (let left = 0; left < count; left += 1) {
    for (let right = left + 1; right < count; right += 1) {
      const leftEvent = graph.events[left] as NormalizedEvent;
      const rightEvent = graph.events[right] as NormalizedEvent;
      if (
        eventsConflict(
          leftEvent,
          rightEvent,
          tokenSets[left] as Set<string>,
          tokenSets[right] as Set<string>,
        )
      ) {
        union.union(left, right);
      }
    }
  }

  const groups = new Map<number, NormalizedEvent[]>();
  graph.events.forEach((event, index) => {
    const root = union.find(index);
    const group = groups.get(root) ?? [];
    group.push(event);
    groups.set(root, group);
  });

  const components = [...groups.values()].map((events) => ({
    events: [...events].sort((left, right) =>
      graph.compareEventIds(left.id, right.id),
    ),
    eventIds: events
      .map((event) => event.id)
      .sort((left, right) => graph.compareEventIds(left, right)),
  }));
  components.sort((left, right) => {
    const leftFirst = left.eventIds[0] ?? "";
    const rightFirst = right.eventIds[0] ?? "";
    return graph.compareEventIds(leftFirst, rightFirst);
  });
  return components.map((component, id) => ({ id, ...component }));
}
