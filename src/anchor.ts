import { Edit, Patch, applyEdit, mapPositionThroughEdit } from './patch';
import { captureContext, findContextMatches } from './context';

/** 锚点：由左右上下文标识文档中的一个位置。 */
export interface Anchor {
  left: string;
  right: string;
  /** 捕获/重捕获上下文时使用的半径 */
  radius: number;
}

/**
 * 迁移结果只有四种状态，绝不猜选：
 * - resolved：唯一确定位置；
 * - ambiguous：多个候选位置（candidates 全部列出）；
 * - tombstone：锚点被删除覆盖且找不到唯一归宿，墓碑已记录；
 * - invalidated：上下文在文档中已不存在（非删除覆盖导致）。
 */
export type AnchorStatus = 'resolved' | 'ambiguous' | 'tombstone' | 'invalidated';

/** 墓碑：删除覆盖锚点时留下的记录，永久保留在状态中。 */
export interface Tombstone {
  /** 删除发生前锚点所在位置 */
  position: number;
  /** 覆盖锚点的被删除文本 */
  deletedText: string;
  /** 可选的版本标签 */
  version?: string;
}

export interface AnchorState {
  anchor: Anchor;
  status: AnchorStatus;
  /** 当前位置（任何状态下都维护，供展示与后续映射） */
  position: number;
  /** status 为 ambiguous 时的全部候选位置 */
  candidates: number[];
  /** 历史墓碑记录，永不清除 */
  tombstones: Tombstone[];
}

export interface MigrateOptions {
  /** 版本标签，会记录到本次产生的墓碑上 */
  version?: string;
}

export interface MigrationResult {
  doc: string;
  state: AnchorState;
}

/** 在文档的指定位置创建锚点。 */
export function createAnchor(doc: string, position: number, radius = 32): AnchorState {
  if (!Number.isInteger(position) || position < 0 || position > doc.length) {
    throw new RangeError(`position ${position} out of bounds for document of length ${doc.length}`);
  }
  if (!Number.isInteger(radius) || radius < 1) {
    throw new RangeError('radius must be a positive integer');
  }
  const { left, right } = captureContext(doc, position, radius);
  return {
    anchor: { left, right, radius },
    status: 'resolved',
    position,
    candidates: [],
    tombstones: [],
  };
}

function resolvedAt(anchor: Anchor, doc: string, position: number, tombstones: Tombstone[]): AnchorState {
  const { left, right } = captureContext(doc, position, anchor.radius);
  return {
    anchor: { left, right, radius: anchor.radius },
    status: 'resolved',
    position,
    candidates: [],
    tombstones,
  };
}

function stepThroughEdit(before: string, edit: Edit, state: AnchorState, version?: string): MigrationResult {
  const after = applyEdit(before, edit);
  const { anchor } = state;

  if (state.status === 'resolved') {
    const mapped = mapPositionThroughEdit(state.position, edit);
    if (mapped.kind === 'mapped') {
      // 位置连续性未断：信任映射结果，并在新位置重新捕获上下文，
      // 使邻近编辑（如同位置插入）被吸收进上下文而不是误判失效。
      return { doc: after, state: resolvedAt(anchor, after, mapped.position, state.tombstones) };
    }
    // 删除覆盖了锚点：必须留下墓碑。
    const tombstone: Tombstone = {
      position: state.position,
      deletedText: before.slice(edit.start, edit.end),
    };
    if (version !== undefined) {
      tombstone.version = version;
    }
    const tombstones = [...state.tombstones, tombstone];
    const matches = findContextMatches(after, anchor.left, anchor.right);
    if (matches.length === 1) {
      // 文本被搬移：唯一上下文匹配，跟随到新位置（墓碑仍保留）。
      return { doc: after, state: resolvedAt(anchor, after, matches[0], tombstones) };
    }
    if (matches.length > 1) {
      // 多个相同上下文：输出全部候选，绝不猜选。
      return {
        doc: after,
        state: { anchor, status: 'ambiguous', position: edit.start, candidates: matches, tombstones },
      };
    }
    return {
      doc: after,
      state: { anchor, status: 'tombstone', position: edit.start, candidates: [], tombstones },
    };
  }

  // 非 resolved 状态：继续映射展示位置，并尝试用上下文恢复。
  const mapped = mapPositionThroughEdit(state.position, edit);
  const position = mapped.kind === 'mapped' ? mapped.position : edit.start;
  const matches = findContextMatches(after, anchor.left, anchor.right);
  if (matches.length === 1) {
    return { doc: after, state: resolvedAt(anchor, after, matches[0], state.tombstones) };
  }
  if (matches.length > 1) {
    return {
      doc: after,
      state: { ...state, status: 'ambiguous', position, candidates: matches },
    };
  }
  return {
    doc: after,
    state: {
      ...state,
      status: state.status === 'ambiguous' ? 'invalidated' : state.status,
      position,
      candidates: [],
    },
  };
}

/**
 * 让锚点迁移穿过一个版本补丁。
 *
 * 迁移是对补丁中每个编辑的折叠（fold），因此
 * migrate(compose(p1, p2)) ≡ migrate(p2) ∘ migrate(p1)，
 * 组合映射与逐步映射必然一致。
 */
export function migrateAnchor(
  doc: string,
  patch: Patch,
  state: AnchorState,
  options: MigrateOptions = {}
): MigrationResult {
  let currentDoc = doc;
  let currentState: AnchorState = {
    ...state,
    anchor: { ...state.anchor },
    candidates: [...state.candidates],
    tombstones: [...state.tombstones],
  };
  for (const edit of patch.edits) {
    const result = stepThroughEdit(currentDoc, edit, currentState, options.version);
    currentDoc = result.doc;
    currentState = result.state;
  }
  return { doc: currentDoc, state: currentState };
}

/** 依次穿过多个连续补丁（逐步映射）。 */
export function migrateThroughPatches(
  doc: string,
  patches: Patch[],
  state: AnchorState,
  options: MigrateOptions = {}
): MigrationResult {
  let currentDoc = doc;
  let currentState = state;
  for (const patch of patches) {
    const result = migrateAnchor(currentDoc, patch, currentState, options);
    currentDoc = result.doc;
    currentState = result.state;
  }
  return { doc: currentDoc, state: currentState };
}

/**
 * 纯上下文重解析：不做位置映射，直接在文档中按左右上下文定位。
 * 用于锚点与文档版本失去同步（例如补丁来自另一分支）的场景。
 * 同样只输出唯一位置、多个候选或失效，绝不猜选。
 */
export function resolveByContext(doc: string, state: AnchorState): AnchorState {
  const matches = findContextMatches(doc, state.anchor.left, state.anchor.right);
  if (matches.length === 1) {
    return resolvedAt(state.anchor, doc, matches[0], state.tombstones);
  }
  if (matches.length > 1) {
    return { ...state, status: 'ambiguous', position: matches[0], candidates: matches };
  }
  return { ...state, status: 'invalidated', candidates: [] };
}
