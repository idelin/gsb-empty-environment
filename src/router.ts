import { TopologyFilter } from './filter.ts';
import type { MemberList } from './member-list.ts';
import type { NodeRecord } from './types.ts';

export interface RouteResult {
  nodeId: string;
  address: string;
  weight: number;
  zone: string;
  role: string;
}

/**
 * 带标签与元数据属性的异构节点成员列表路由。
 * 基于属性筛选器圈定动态拓扑子集，再在子集内按权重择优。
 */
export class Router {
  private readonly memberList: MemberList;

  constructor(memberList: MemberList) {
    this.memberList = memberList;
  }

  /** 返回满足筛选表达式的健康节点，按权重降序（同权重按 nodeId 升序保证确定性） */
  query(expression: string): RouteResult[] {
    const filter = new TopologyFilter(expression);
    return filter
      .select(this.memberList.healthyRecords())
      .sort(Router.byWeightDesc)
      .map(Router.toResult);
  }

  /** 返回满足筛选表达式的、权重最高的健康节点；无匹配时返回 null */
  route(expression: string): RouteResult | null {
    return this.query(expression)[0] ?? null;
  }

  private static byWeightDesc(a: NodeRecord, b: NodeRecord): number {
    if (b.metadata.weight !== a.metadata.weight) return b.metadata.weight - a.metadata.weight;
    return a.nodeId < b.nodeId ? -1 : 1;
  }

  private static toResult(record: NodeRecord): RouteResult {
    return {
      nodeId: record.nodeId,
      address: record.address,
      weight: record.metadata.weight,
      zone: record.metadata.zone,
      role: record.metadata.role,
    };
  }
}
