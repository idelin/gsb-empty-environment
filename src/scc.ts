// Tarjan 强连通分量：依赖环中的项目必须同批发布

export interface SccResult {
  compOf: number[]; // 节点 -> 分量编号
  comps: number[][]; // 分量编号 -> 节点列表
}

export function tarjanScc(n: number, adj: number[][]): SccResult {
  const index = new Array<number>(n).fill(-1);
  const low = new Array<number>(n).fill(0);
  const onStack: boolean[] = [];
  for (let i = 0; i < n; i++) onStack.push(false);
  const stack: number[] = [];
  const compOf = new Array<number>(n).fill(-1);
  const comps: number[][] = [];
  let counter = 0;

  const strongconnect = (v: number): void => {
    index[v] = counter;
    low[v] = counter;
    counter++;
    stack.push(v);
    onStack[v] = true;
    for (const w of adj[v]) {
      if (index[w] === -1) {
        strongconnect(w);
        low[v] = Math.min(low[v], low[w]);
      } else if (onStack[w]) {
        low[v] = Math.min(low[v], index[w]);
      }
    }
    if (low[v] === index[v]) {
      const comp: number[] = [];
      for (;;) {
        const w = stack.pop();
        if (w === undefined) break;
        onStack[w] = false;
        compOf[w] = comps.length;
        comp.push(w);
        if (w === v) break;
      }
      comps.push(comp);
    }
  };

  for (let v = 0; v < n; v++) {
    if (index[v] === -1) strongconnect(v);
  }
  return { compOf, comps };
}
