"use strict";
Object.defineProperty(exports, "__esModule", { value: true });
exports.mapPosition = exports.mapPositionThroughEdit = exports.composePatches = exports.applyPatch = exports.applyEdit = exports.validateEdit = exports.PatchError = void 0;
class PatchError extends Error {
}
exports.PatchError = PatchError;
function validateEdit(doc, edit) {
    if (!Number.isInteger(edit.start) || !Number.isInteger(edit.end)) {
        throw new PatchError(`edit offsets must be integers, got [${edit.start}, ${edit.end})`);
    }
    if (edit.start < 0 || edit.end < edit.start || edit.end > doc.length) {
        throw new PatchError(`edit [${edit.start}, ${edit.end}) out of bounds for document of length ${doc.length}`);
    }
}
exports.validateEdit = validateEdit;
function applyEdit(doc, edit) {
    validateEdit(doc, edit);
    return doc.slice(0, edit.start) + edit.text + doc.slice(edit.end);
}
exports.applyEdit = applyEdit;
function applyPatch(doc, patch) {
    let out = doc;
    for (const edit of patch.edits) {
        out = applyEdit(out, edit);
    }
    return out;
}
exports.applyPatch = applyPatch;
/**
 * 组合补丁：把多个连续补丁合并为一个。
 * 由于编辑本来就是按顺序解释的，组合就是编辑序列的连接；
 * 这保证了「组合映射 ≡ 逐步映射」。
 */
function composePatches(...patches) {
    const edits = [];
    for (const patch of patches) {
        for (const edit of patch.edits) {
            edits.push(edit);
        }
    }
    return { edits };
}
exports.composePatches = composePatches;
/**
 * 单个编辑下的位置映射。规则（编辑区间为 [start, end)，替换为 text）：
 * - position < start：不变；
 * - position > end：平移 delta；
 * - position === start 且为纯插入（start === end）：锚点保持在插入文本之前（左亲和）；
 * - position === start 且为删除/替换：锚点幸存，停留在区间起点；
 * - position === end（end > start）：锚点落在替换文本之后；
 * - start < position < end：位置被删除覆盖，返回 tombstone。
 */
function mapPositionThroughEdit(position, edit) {
    const { start, end, text } = edit;
    if (position < start) {
        return { kind: 'mapped', position };
    }
    if (position > end) {
        return { kind: 'mapped', position: position - (end - start) + text.length };
    }
    if (position === start) {
        return { kind: 'mapped', position: start };
    }
    if (position === end) {
        return { kind: 'mapped', position: start + text.length };
    }
    return { kind: 'tombstone' };
}
exports.mapPositionThroughEdit = mapPositionThroughEdit;
/** 依次穿过整个补丁的位置映射；一旦进入墓碑态便保持。 */
function mapPosition(position, patch) {
    let current = { kind: 'mapped', position };
    for (const edit of patch.edits) {
        if (current.kind === 'tombstone') {
            break;
        }
        current = mapPositionThroughEdit(current.position, edit);
    }
    return current;
}
exports.mapPosition = mapPosition;
