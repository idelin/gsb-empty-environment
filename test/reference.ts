import { decodeMessage } from '../src/wire.ts';
import type { SchemaRegistry } from '../src/registry.ts';
import type { Disclosure, ErrorNode, FieldValue, ViewerContext } from '../src/types.ts';

export interface ReferenceOutput {
  text: string;
  truncated: boolean;
  nodesEmitted: number;
}

/**
 * 独立小图投影解释器。
 * 与 src/project.ts 不共享任何投影 / 渲染 / 预算代码：
 * 递归解释可见子图，按同一外部帧格式约定独立渲染，用于成对对照。
 */
export function referenceProject(
  root: ErrorNode,
  viewer: ViewerContext,
  registry: SchemaRegistry,
  budgets: { maxBytes: number; maxNodes: number },
): ReferenceOutput {
  const shown: ErrorNode[] = [];
  const table = new Map<
    ErrorNode,
    {
      isolated: boolean;
      fields: Array<[string, FieldValue]>;
      causes: number[];
      details: number[];
    }
  >();

  const ok = (d: Disclosure, tenant: string): boolean => {
    if (d.kind === 'public') return true;
    if (d.kind === 'tenant') return viewer.tenant === tenant;
    return viewer.clearance >= d.level;
  };

  const walk = (n: ErrorNode): number | null => {
    if (!ok(n.visibility, n.tenant)) return null;
    const seen = shown.indexOf(n);
    if (seen !== -1) return seen;
    const me = shown.length;
    shown.push(n);
    const rec = {
      isolated: false,
      fields: [] as Array<[string, FieldValue]>,
      causes: [] as number[],
      details: [] as number[],
    };
    table.set(n, rec);
    const schema = registry.lookup(n.typeUrl);
    if (schema === undefined) {
      rec.isolated = true;
    } else {
      let decoded: Array<[string, FieldValue]> | null = null;
      try {
        decoded = decodeMessage(schema, n.raw);
      } catch {
        decoded = null;
      }
      if (decoded === null) {
        rec.isolated = true;
      } else {
        for (const [name, value] of decoded) {
          const d = n.disclose[name];
          if (d !== undefined && ok(d, n.tenant)) rec.fields.push([name, value]);
        }
      }
    }
    for (const c of n.causes) {
      const o = walk(c);
      if (o !== null) rec.causes.push(o);
    }
    for (const d of n.details) {
      const o = walk(d);
      if (o !== null) rec.details.push(o);
    }
    return me;
  };
  walk(root);

  const canon = (v: FieldValue): string => {
    if (v === null) return 'null';
    if (typeof v === 'string') return JSON.stringify(v);
    if (typeof v === 'number' || typeof v === 'boolean') return String(v);
    if (v instanceof Uint8Array) {
      return `{"$bytes":"${Buffer.from(v).toString('base64')}"}`;
    }
    if (Array.isArray(v)) return `[${v.map(canon).join(',')}]`;
    const keys = Object.keys(v).sort();
    return `{${keys.map((k) => `${JSON.stringify(k)}:${canon(v[k])}`).join(',')}}`;
  };

  const lines: string[] = [`["H",${registry.version}]`];
  shown.forEach((n, i) => {
    const rec = table.get(n);
    if (rec === undefined) throw new Error('reference: missing node record');
    lines.push(JSON.stringify(['N', i, n.service, n.tenant, n.typeUrl, rec.isolated ? 1 : 0]));
    for (const [name, value] of rec.fields) {
      lines.push(`["F",${i},${JSON.stringify(name)},${canon(value)}]`);
    }
    for (const c of rec.causes) lines.push(`["C",${i},${c}]`);
    for (const d of rec.details) lines.push(`["D",${i},${d}]`);
  });

  let out = '';
  let used = 0;
  let emitted = 0;
  let i = 0;
  for (; i < lines.length; i++) {
    const size = Buffer.byteLength(lines[i], 'utf8') + 1;
    const isNode = lines[i][2] === 'N';
    if (isNode && emitted >= budgets.maxNodes) break;
    if (used + size > budgets.maxBytes) break;
    out += `${lines[i]}\n`;
    used += size;
    if (isNode) emitted += 1;
  }
  return { text: out, truncated: i < lines.length, nodesEmitted: emitted };
}
