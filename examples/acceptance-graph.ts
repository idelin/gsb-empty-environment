import { Graph } from "../src/graph.ts";

/**
 * 验收用图：密集子网 + 悬挂链。
 *
 *  - 节点 0-4  : K5 稠密团          -> 核度 4
 *  - 节点 5-8  : K4 稠密团          -> 核度 3（经单边 0-5 挂在 K5 上，核度不变）
 *  - 节点 9-12 : K4 缺一条边 (9,10) -> 核度 2（补上 (9,10) 即成 K4，核度升 3）
 *  - 节点 13-15: 悬挂链 1-13-14-15  -> 核度 1
 */
export function buildAcceptanceGraph(): Graph {
  const g = new Graph();
  const clique = (vs: number[]) => {
    for (let i = 0; i < vs.length; i++) {
      for (let j = i + 1; j < vs.length; j++) g.addEdge(vs[i]!, vs[j]!);
    }
  };
  clique([0, 1, 2, 3, 4]); // K5，核度 4
  clique([5, 6, 7, 8]); // K4，核度 3
  g.addEdge(0, 5); // 子网间单边连接，不改变各自核度
  clique([9, 10, 11, 12]);
  g.removeEdge(9, 10); // K4 缺边 (9,10)，核度 2
  g.addEdge(1, 13); // 悬挂链
  g.addEdge(13, 14);
  g.addEdge(14, 15);
  return g;
}

export const EXPECTED_CORES: ReadonlyMap<number, number> = new Map([
  [0, 4], [1, 4], [2, 4], [3, 4], [4, 4],
  [5, 3], [6, 3], [7, 3], [8, 3],
  [9, 2], [10, 2], [11, 2], [12, 2],
  [13, 1], [14, 1], [15, 1],
]);
