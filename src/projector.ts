/**
 * 结构化 JSON 扁平化列式投影器。
 *
 * - flatten(json)：全量推断，把所有叶子路径抽取为独立虚拟列。
 * - project(json, paths)：按需投影。基于原始文本的流式扫描，
 *   只物化被请求路径上的叶子值；无关子树通过 skipValue 纯指针跳过，
 *   不触发 JSON.parse、不构造任何中间 JS 对象。
 */
import { ColumnStore } from './column-store';
import { buildTrie, joinField, matchArrayElement, PathTrieNode } from './path';
import { JsonScanner } from './scanner';

export interface ProjectionStats {
  /** 输入 JSON 文本总字节数（string length）。 */
  totalBytes: number;
  /** 被跳过的无关子树所占字节数。 */
  skippedBytes: number;
  /** 被跳过的无关子树个数。 */
  skippedSubtrees: number;
  /** 实际物化的叶子值个数。 */
  materializedValues: number;
}

export interface ProjectionResult {
  store: ColumnStore;
  stats: ProjectionStats;
}

interface Ctx {
  sc: JsonScanner;
  store: ColumnStore;
  stats: ProjectionStats;
}

function newStats(totalBytes: number): ProjectionStats {
  return { totalBytes, skippedBytes: 0, skippedSubtrees: 0, materializedValues: 0 };
}

function skipIgnored(ctx: Ctx): void {
  const sc = ctx.sc;
  sc.skipWs();
  const start = sc.pos;
  sc.skipValue();
  ctx.stats.skippedBytes += sc.pos - start;
  ctx.stats.skippedSubtrees++;
}

/** 物化“被请求路径终止处”的子树：仅对该小子串调用 JSON.parse。 */
function captureSubtree(ctx: Ctx, path: string, row: number): void {
  const sc = ctx.sc;
  sc.skipWs();
  const start = sc.pos;
  sc.skipValue();
  const raw = sc.text.slice(start, sc.pos);
  ctx.store.addValue(path, row, JSON.parse(raw));
  ctx.stats.materializedValues++;
}

function walkValue(ctx: Ctx, node: PathTrieNode, path: string, row: number): void {
  const sc = ctx.sc;
  const c = sc.peek();
  if (c === '{') {
    if (node.terminal) return captureSubtree(ctx, path, row);
    sc.pos++;
    if (sc.peek() === '}') {
      sc.pos++;
      return;
    }
    for (;;) {
      sc.skipWs();
      const key = sc.readString();
      sc.expect(':');
      const child = node.children.get(key);
      if (child) {
        walkValue(ctx, child, joinField(path, key), row);
      } else {
        skipIgnored(ctx);
      }
      const sep = sc.peek();
      if (sep === ',') {
        sc.pos++;
        continue;
      }
      if (sep === '}') {
        sc.pos++;
        return;
      }
      throw sc.error(`Expected ',' or '}' but found '${sep ?? '<eof>'}'`);
    }
  }
  if (c === '[') {
    if (node.terminal) return captureSubtree(ctx, path, row);
    sc.pos++;
    if (sc.peek() === ']') {
      sc.pos++;
      return;
    }
    let index = 0;
    for (;;) {
      const matches = matchArrayElement(node, index);
      if (matches.length === 0) {
        skipIgnored(ctx);
      } else if (matches.length === 1) {
        const m = matches[0];
        walkValue(ctx, m.node, path + (m.via === 'wildcard' ? '[]' : `[${index}]`), row);
      } else {
        // 定向下标与通配同时命中：记录子串起点，分别以各自路径标签扫描两遍。
        sc.skipWs();
        const start = sc.pos;
        for (const m of matches) {
          sc.pos = start;
          walkValue(ctx, m.node, path + (m.via === 'wildcard' ? '[]' : `[${index}]`), row);
        }
      }
      index++;
      const sep = sc.peek();
      if (sep === ',') {
        sc.pos++;
        continue;
      }
      if (sep === ']') {
        sc.pos++;
        return;
      }
      throw sc.error(`Expected ',' or ']' but found '${sep ?? '<eof>'}'`);
    }
  }
  if (node.terminal) {
    ctx.store.addValue(path, row, sc.readScalar());
    ctx.stats.materializedValues++;
  } else {
    skipIgnored(ctx);
  }
}

/** 全量推断模式：每个叶子标量都物化，数组元素统一归入 [] 通配路径。 */
function walkAll(ctx: Ctx, path: string, row: number): void {
  const sc = ctx.sc;
  const c = sc.peek();
  if (c === '{') {
    sc.pos++;
    if (sc.peek() === '}') {
      sc.pos++;
      ctx.store.addValue(path, row, {});
      ctx.stats.materializedValues++;
      return;
    }
    for (;;) {
      sc.skipWs();
      const key = sc.readString();
      sc.expect(':');
      walkAll(ctx, joinField(path, key), row);
      const sep = sc.peek();
      if (sep === ',') {
        sc.pos++;
        continue;
      }
      if (sep === '}') {
        sc.pos++;
        return;
      }
      throw sc.error(`Expected ',' or '}' but found '${sep ?? '<eof>'}'`);
    }
  }
  if (c === '[') {
    sc.pos++;
    if (sc.peek() === ']') {
      sc.pos++;
      ctx.store.addValue(path, row, []);
      ctx.stats.materializedValues++;
      return;
    }
    for (;;) {
      walkAll(ctx, path + '[]', row);
      const sep = sc.peek();
      if (sep === ',') {
        sc.pos++;
        continue;
      }
      if (sep === ']') {
        sc.pos++;
        return;
      }
      throw sc.error(`Expected ',' or ']' but found '${sep ?? '<eof>'}'`);
    }
  }
  ctx.store.addValue(path, row, sc.readScalar());
  ctx.stats.materializedValues++;
}

function walkRoot(json: string, walk: (ctx: Ctx, path: string, row: number) => void): ProjectionResult {
  const store = new ColumnStore();
  const stats = newStats(json.length);
  const sc = new JsonScanner(json);
  const ctx: Ctx = { sc, store, stats };
  if (sc.peek() === '[') {
    // 根数组：每个元素视为一条记录（一行）。
    sc.pos++;
    if (sc.peek() === ']') {
      sc.pos++;
    } else {
      let row = 0;
      for (;;) {
        walk(ctx, '', row);
        row++;
        const sep = sc.peek();
        if (sep === ',') {
          sc.pos++;
          continue;
        }
        if (sep === ']') {
          sc.pos++;
          break;
        }
        throw sc.error(`Expected ',' or ']' but found '${sep ?? '<eof>'}'`);
      }
      store.rowCount = row;
    }
  } else {
    walk(ctx, '', 0);
    store.rowCount = 1;
  }
  sc.skipWs();
  if (!sc.eof) throw sc.error('Unexpected trailing content');
  return { store, stats };
}

/**
 * 按需投影：仅物化 paths 指定的叶子路径对应的虚拟列。
 * 未命中的子树被整体跳过，不做任何反序列化。
 */
export function project(json: string, paths: string[]): ProjectionResult {
  const trie = buildTrie(paths);
  return walkRoot(json, (ctx, _path, row) => walkValue(ctx, trie, '', row));
}

/** 全量扁平化：自动推断所有叶子路径并抽取为虚拟列。 */
export function flatten(json: string): ProjectionResult {
  return walkRoot(json, (ctx, path, row) => walkAll(ctx, path, row));
}

/** 在已解析的 JS 值上做同样的投影（用于交叉验证或对象来源的场景）。 */
export function projectValue(value: unknown, paths: string[]): ColumnStore {
  const trie = buildTrie(paths);
  const store = new ColumnStore();
  const walk = (v: unknown, node: PathTrieNode, path: string, row: number): void => {
    if (node.terminal) {
      store.addValue(path, row, v);
      return;
    }
    if (Array.isArray(v)) {
      for (let i = 0; i < v.length; i++) {
        for (const m of matchArrayElement(node, i)) {
          walk(v[i], m.node, path + (m.via === 'wildcard' ? '[]' : `[${i}]`), row);
        }
      }
      return;
    }
    if (v !== null && typeof v === 'object') {
      for (const [key, child] of node.children) {
        if (Object.prototype.hasOwnProperty.call(v, key)) {
          walk((v as Record<string, unknown>)[key], child, joinField(path, key), row);
        }
      }
      return;
    }
    // 标量但路径未终止：请求路径越过了标量，忽略。
  };
  if (Array.isArray(value)) {
    value.forEach((record, row) => walk(record, trie, '', row));
    store.rowCount = value.length;
  } else {
    walk(value, trie, '', 0);
    store.rowCount = 1;
  }
  return store;
}
