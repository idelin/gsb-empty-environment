"use strict";
/** 左右上下文工具：锚点身份由「位置左侧文本」和「位置右侧文本」共同确定。 */
Object.defineProperty(exports, "__esModule", { value: true });
exports.captureContext = exports.contextMatchesAt = exports.findContextMatches = void 0;
/** 在文档中找出所有「左上下文结束、右上下文开始」的位置。 */
function findContextMatches(doc, left, right) {
    const needle = left + right;
    if (needle.length === 0) {
        const all = [];
        for (let i = 0; i <= doc.length; i += 1) {
            all.push(i);
        }
        return all;
    }
    const positions = [];
    let index = doc.indexOf(needle);
    while (index !== -1) {
        positions.push(index + left.length);
        index = doc.indexOf(needle, index + 1);
    }
    return positions;
}
exports.findContextMatches = findContextMatches;
/** 校验指定位置的左右上下文是否精确匹配。 */
function contextMatchesAt(doc, position, left, right) {
    return (position >= left.length &&
        position + right.length <= doc.length &&
        doc.slice(position - left.length, position) === left &&
        doc.slice(position, position + right.length) === right);
}
exports.contextMatchesAt = contextMatchesAt;
/** 在指定位置按半径捕获左右上下文（文档边界处会缩短）。 */
function captureContext(doc, position, radius) {
    return {
        left: doc.slice(Math.max(0, position - radius), position),
        right: doc.slice(position, Math.min(doc.length, position + radius)),
    };
}
exports.captureContext = captureContext;
