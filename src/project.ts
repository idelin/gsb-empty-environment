import { createHash } from 'node:crypto';
import type { SchemaRegistry } from './registry.ts';
import { decodeMessage } from './wire.ts';
import { toCanonical } from './canonical.ts';
import { CursorError, signCursor, verifyCursor, type CursorPayload } from './cursor.ts';
import type { Disclosure, ErrorNode, FieldValue, ViewerContext } from './types.ts';

/** 投影时使用的可见性判定。 */
export interface VisibilityPolicy {
  nodeVisible(node: ErrorNode): boolean;
  fieldVisible(node: ErrorNode, field: string): boolean;
}

export function disclosurePermits(
  d: Disclosure,
  viewer: ViewerContext,
  nodeTenant: string,
): boolean {
  switch (d.kind) {
    case 'public':
      return true;
    case 'tenant':
      return viewer.tenant === nodeTenant;
    case 'restricted':
      return viewer.clearance >= d.level;
  }
}

export function viewerPolicy(viewer: ViewerContext): VisibilityPolicy {
  return {
    nodeVisible: (node) => disclosurePermits(node.visibility, viewer, node.tenant),
    fieldVisible: (node, field) => {
      const d = node.disclose[field];
      return d !== undefined && disclosurePermits(d, viewer, node.tenant);
    },
  };
}

/** 仅供内部完整诊断使用，绝不对客户端输出。 */
export const allowAllPolicy: VisibilityPolicy = {
  nodeVisible: () => true,
  fieldVisible: () => true,
};

export interface PlannedNode {
  ordinal: number;
  service: string;
  tenant: string;
  typeUrl: string;
  /** 未知类型或解码失败：隔离，不输出任何字段。 */
  isolated: boolean;
  fields: Array<[string, FieldValue]>;
  causes: number[];
  details: number[];
}

/**
 * 投影遍历：DFS 先序、先 causes 后 details、边按声明顺序。
 * 只访问可见节点，因此序号、原因数量与去重结果完全由可见子图决定；
 * 共享节点与环通过首次访问序号去重。
 */
export function buildProjectionPlan(
  root: ErrorNode,
  registry: SchemaRegistry,
  policy: VisibilityPolicy,
): PlannedNode[] {
  const nodes: PlannedNode[] = [];
  const assigned = new Map<ErrorNode, number>();

  const visit = (node: ErrorNode): number | null => {
    if (!policy.nodeVisible(node)) return null;
    const existing = assigned.get(node);
    if (existing !== undefined) return existing;
    const ordinal = nodes.length;
    const planned: PlannedNode = {
      ordinal,
      service: node.service,
      tenant: node.tenant,
      typeUrl: node.typeUrl,
      isolated: false,
      fields: [],
      causes: [],
      details: [],
    };
    assigned.set(node, ordinal);
    nodes.push(planned);

    const schema = registry.lookup(node.typeUrl);
    if (schema === undefined) {
      planned.isolated = true;
    } else {
      try {
        for (const [name, value] of decodeMessage(schema, node.raw)) {
          if (policy.fieldVisible(node, name)) planned.fields.push([name, value]);
        }
      } catch {
        planned.isolated = true;
        planned.fields.length = 0;
      }
    }

    for (const cause of node.causes) {
      const target = visit(cause);
      if (target !== null) planned.causes.push(target);
    }
    for (const detail of node.details) {
      const target = visit(detail);
      if (target !== null) planned.details.push(target);
    }
    return ordinal;
  };

  visit(root);
  return nodes;
}

/**
 * 规范序列化：每行一个 JSON 帧。
 * H=头部（注册表版本），N=节点，F=字段，C=原因边，D=详情边。
 * 每个字段独占一帧，因此任何帧边界都是字段边界。
 */
export function renderPlanFrames(nodes: PlannedNode[], registryVersion: number): string[] {
  const frames: string[] = [JSON.stringify(['H', registryVersion])];
  for (const n of nodes) {
    frames.push(
      JSON.stringify(['N', n.ordinal, n.service, n.tenant, n.typeUrl, n.isolated ? 1 : 0]),
    );
    for (const [name, value] of n.fields) {
      frames.push(
        `["F",${n.ordinal},${JSON.stringify(name)},${JSON.stringify(toCanonical(value))}]`,
      );
    }
    for (const cause of n.causes) frames.push(JSON.stringify(['C', n.ordinal, cause]));
    for (const detail of n.details) frames.push(JSON.stringify(['D', n.ordinal, detail]));
  }
  return frames;
}

export interface Budgets {
  /** 按投影后的规范序列化字节数计量。 */
  maxBytes: number;
  maxNodes: number;
}

export interface ProjectionResult {
  text: string;
  bytes: Uint8Array;
  truncated: boolean;
  nodesEmitted: number;
  bytesEmitted: number;
  /** 截断时的可核验继续游标；未截断为 null。 */
  cursor: string | null;
}

const encoder = new TextEncoder();

interface Emission {
  text: string;
  nextFrame: number;
  nodesEmitted: number;
  truncated: boolean;
}

function emitFrames(frames: string[], startFrame: number, budgets: Budgets): Emission {
  let text = '';
  let used = 0;
  let nodes = 0;
  let i = startFrame;
  for (; i < frames.length; i++) {
    const frame = frames[i];
    const isNode = frame.startsWith('["N"');
    if (isNode && nodes >= budgets.maxNodes) break;
    const size = encoder.encode(frame).length + 1;
    if (used + size > budgets.maxBytes) break;
    text += `${frame}\n`;
    used += size;
    if (isNode) nodes += 1;
  }
  return { text, nextFrame: i, nodesEmitted: nodes, truncated: i < frames.length };
}

function planDigest(frames: string[]): string {
  const hash = createHash('sha256');
  for (const frame of frames) hash.update(`${frame}\n`);
  return hash.digest('hex');
}

function finish(
  frames: string[],
  offset: number,
  budgets: Budgets,
  key: string,
  viewer: ViewerContext,
  registry: SchemaRegistry,
): ProjectionResult {
  const emission = emitFrames(frames, offset, budgets);
  let cursor: string | null = null;
  if (emission.truncated) {
    const payload: CursorPayload = {
      v: 1,
      tenant: viewer.tenant,
      registry: registry.version,
      generation: viewer.generation,
      offset: emission.nextFrame,
      plan: planDigest(frames),
    };
    cursor = signCursor(payload, key);
  }
  const bytes = encoder.encode(emission.text);
  return {
    text: emission.text,
    bytes,
    truncated: emission.truncated,
    nodesEmitted: emission.nodesEmitted,
    bytesEmitted: bytes.length,
    cursor,
  };
}

/** 从根错误投影调用者可见的子图，并在预算内序列化。 */
export function projectErrorGraph(
  root: ErrorNode,
  viewer: ViewerContext,
  registry: SchemaRegistry,
  budgets: Budgets,
  key: string,
): ProjectionResult {
  const plan = buildProjectionPlan(root, registry, viewerPolicy(viewer));
  const frames = renderPlanFrames(plan, registry.version);
  return finish(frames, 0, budgets, key, viewer, registry);
}

/**
 * 凭游标继续投影。游标必须与当前租户、注册表版本、权限代次一致，
 * 且完整投影计划摘要一致，否则抛 CursorError。
 */
export function continueProjection(
  root: ErrorNode,
  viewer: ViewerContext,
  registry: SchemaRegistry,
  budgets: Budgets,
  key: string,
  cursor: string,
): ProjectionResult {
  const payload = verifyCursor(cursor, key, {
    tenant: viewer.tenant,
    registry: registry.version,
    generation: viewer.generation,
  });
  const plan = buildProjectionPlan(root, registry, viewerPolicy(viewer));
  const frames = renderPlanFrames(plan, registry.version);
  if (planDigest(frames) !== payload.plan) {
    throw new CursorError('cursor does not match projection plan');
  }
  if (payload.offset > frames.length) {
    throw new CursorError('cursor offset out of range');
  }
  return finish(frames, payload.offset, budgets, key, viewer, registry);
}
