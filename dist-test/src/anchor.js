"use strict";
Object.defineProperty(exports, "__esModule", { value: true });
exports.resolveByContext = exports.migrateThroughPatches = exports.migrateAnchor = exports.createAnchor = void 0;
const patch_1 = require("./patch");
const context_1 = require("./context");
/** 在文档的指定位置创建锚点。 */
function createAnchor(doc, position, radius = 32) {
    if (!Number.isInteger(position) || position < 0 || position > doc.length) {
        throw new RangeError(`position ${position} out of bounds for document of length ${doc.length}`);
    }
    if (!Number.isInteger(radius) || radius < 1) {
        throw new RangeError('radius must be a positive integer');
    }
    const { left, right } = (0, context_1.captureContext)(doc, position, radius);
    return {
        anchor: { left, right, radius },
        status: 'resolved',
        position,
        candidates: [],
        tombstones: [],
    };
}
exports.createAnchor = createAnchor;
function resolvedAt(anchor, doc, position, tombstones) {
    const { left, right } = (0, context_1.captureContext)(doc, position, anchor.radius);
    return {
        anchor: { left, right, radius: anchor.radius },
        status: 'resolved',
        position,
        candidates: [],
        tombstones,
    };
}
function stepThroughEdit(before, edit, state, version) {
    const after = (0, patch_1.applyEdit)(before, edit);
    const { anchor } = state;
    if (state.status === 'resolved') {
        const mapped = (0, patch_1.mapPositionThroughEdit)(state.position, edit);
        if (mapped.kind === 'mapped') {
            // 位置连续性未断：信任映射结果，并在新位置重新捕获上下文，
            // 使邻近编辑（如同位置插入）被吸收进上下文而不是误判失效。
            return { doc: after, state: resolvedAt(anchor, after, mapped.position, state.tombstones) };
        }
        // 删除覆盖了锚点：必须留下墓碑。
        const tombstone = {
            position: state.position,
            deletedText: before.slice(edit.start, edit.end),
        };
        if (version !== undefined) {
            tombstone.version = version;
        }
        const tombstones = [...state.tombstones, tombstone];
        const matches = (0, context_1.findContextMatches)(after, anchor.left, anchor.right);
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
    const mapped = (0, patch_1.mapPositionThroughEdit)(state.position, edit);
    const position = mapped.kind === 'mapped' ? mapped.position : edit.start;
    const matches = (0, context_1.findContextMatches)(after, anchor.left, anchor.right);
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
function migrateAnchor(doc, patch, state, options = {}) {
    let currentDoc = doc;
    let currentState = {
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
exports.migrateAnchor = migrateAnchor;
/** 依次穿过多个连续补丁（逐步映射）。 */
function migrateThroughPatches(doc, patches, state, options = {}) {
    let currentDoc = doc;
    let currentState = state;
    for (const patch of patches) {
        const result = migrateAnchor(currentDoc, patch, currentState, options);
        currentDoc = result.doc;
        currentState = result.state;
    }
    return { doc: currentDoc, state: currentState };
}
exports.migrateThroughPatches = migrateThroughPatches;
/**
 * 纯上下文重解析：不做位置映射，直接在文档中按左右上下文定位。
 * 用于锚点与文档版本失去同步（例如补丁来自另一分支）的场景。
 * 同样只输出唯一位置、多个候选或失效，绝不猜选。
 */
function resolveByContext(doc, state) {
    const matches = (0, context_1.findContextMatches)(doc, state.anchor.left, state.anchor.right);
    if (matches.length === 1) {
        return resolvedAt(state.anchor, doc, matches[0], state.tombstones);
    }
    if (matches.length > 1) {
        return { ...state, status: 'ambiguous', position: matches[0], candidates: matches };
    }
    return { ...state, status: 'invalidated', candidates: [] };
}
exports.resolveByContext = resolveByContext;
