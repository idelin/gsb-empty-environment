/** 左右上下文工具：锚点身份由「位置左侧文本」和「位置右侧文本」共同确定。 */

/** 在文档中找出所有「左上下文结束、右上下文开始」的位置。 */
export function findContextMatches(doc: string, left: string, right: string): number[] {
  const needle = left + right;
  if (needle.length === 0) {
    const all: number[] = [];
    for (let i = 0; i <= doc.length; i += 1) {
      all.push(i);
    }
    return all;
  }
  const positions: number[] = [];
  let index = doc.indexOf(needle);
  while (index !== -1) {
    positions.push(index + left.length);
    index = doc.indexOf(needle, index + 1);
  }
  return positions;
}

/** 校验指定位置的左右上下文是否精确匹配。 */
export function contextMatchesAt(
  doc: string,
  position: number,
  left: string,
  right: string
): boolean {
  return (
    position >= left.length &&
    position + right.length <= doc.length &&
    doc.slice(position - left.length, position) === left &&
    doc.slice(position, position + right.length) === right
  );
}

/** 在指定位置按半径捕获左右上下文（文档边界处会缩短）。 */
export function captureContext(
  doc: string,
  position: number,
  radius: number
): { left: string; right: string } {
  return {
    left: doc.slice(Math.max(0, position - radius), position),
    right: doc.slice(position, Math.min(doc.length, position + radius)),
  };
}
