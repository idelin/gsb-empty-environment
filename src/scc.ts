/**
 * 依赖图工具：Tarjan 强连通分量 + 环见证提取。
 * 约定：prereqsOf[u] 是 u 直接依赖（需要先发布）的项目下标列表。
 */

/** Tarjan 算法，返回每个强连通分量的成员下标数组。 */
export function stronglyConnectedComponents(nodeCount: number, prereqsOf: number[][]): number[][] {
  const indexOf = new Int32Array(nodeCount).fill(-1);
  const lowlink = new Int32Array(nodeCount);
  const onStack = new Array<boolean>(nodeCount).fill(false);
  const stack: number[] = [];
  const components: number[][] = [];
  let nextIndex = 0;

  const strongConnect = (start: number): void => {
    // 显式栈避免深递归（n ≤ 18，其实递归也行，这里写迭代版更稳）
    const frames: Array<{ node: number; childPos: number }> = [{ node: start, childPos: 0 }];
    indexOf[start] = lowlink[start] = nextIndex++;
    stack.push(start);
    onStack[start] = true;
    while (frames.length > 0) {
      const frame = frames[frames.length - 1];
      const node = frame.node;
      if (frame.childPos < prereqsOf[node].length) {
        const child = prereqsOf[node][frame.childPos++];
        if (indexOf[child] === -1) {
          indexOf[child] = lowlink[child] = nextIndex++;
          stack.push(child);
          onStack[child] = true;
          frames.push({ node: child, childPos: 0 });
        } else if (onStack[child]) {
          lowlink[node] = Math.min(lowlink[node], indexOf[child]);
        }
      } else {
        frames.pop();
        if (frames.length > 0) {
          const parent = frames[frames.length - 1].node;
          lowlink[parent] = Math.min(lowlink[parent], lowlink[node]);
        }
        if (lowlink[node] === indexOf[node]) {
          const component: number[] = [];
          for (;;) {
            const member = stack.pop() as number;
            onStack[member] = false;
            component.push(member);
            if (member === node) break;
          }
          components.push(component);
        }
      }
    }
  };

  for (let node = 0; node < nodeCount; node++) {
    if (indexOf[node] === -1) strongConnect(node);
  }
  return components;
}

/**
 * 在同一个强连通分量内部找一条具体依赖环（作为不可行/同批发布的证据）。
 * 返回下标路径 [a, b, ..., a]，首尾相同；分量大小 ≥ 2 时必然存在。
 */
export function findCycleWithin(members: number[], prereqsOf: number[][]): number[] {
  const inSet = new Set(members);
  const start = members[0];
  // 从 start 出发 DFS（只走分量内部边），第一次遇到指回 start 的边即得环。
  const parent = new Map<number, number>();
  const visited = new Set<number>([start]);
  const stack: number[] = [start];
  while (stack.length > 0) {
    const node = stack.pop() as number;
    for (const next of prereqsOf[node]) {
      if (!inSet.has(next)) continue;
      if (next === start) {
        const cycle = [start];
        let cur = node;
        while (cur !== start) {
          cycle.push(cur);
          cur = parent.get(cur) as number;
        }
        cycle.push(start);
        return cycle;
      }
      if (!visited.has(next)) {
        visited.add(next);
        parent.set(next, node);
        stack.push(next);
      }
    }
  }
  // 强连通分量（大小 ≥ 2）中必能回到起点，正常不会走到这里。
  return [start, start];
}
