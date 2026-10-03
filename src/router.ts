import { MemberList } from './memberlist.ts';
import type { AttributeFilter, MemberNode } from './types.ts';

/**
 * 基于属性的动态拓扑子集筛选器 + 路由器。
 *
 * 筛选条件语法：`{ 'zone': 'us-east', 'labels.tier': 'gold', 'role': 'follower' }`
 * 仅健康节点参与路由；多候选时选择权重最高者（并列时按节点 ID 确定性选择）。
 */
export class Router {
  private readonly members: MemberList;

  constructor(members: MemberList) {
    this.members = members;
  }

  /** 按属性筛选出拓扑子集（默认仅健康节点） */
  select(filter: AttributeFilter, opts: { healthyOnly?: boolean } = {}): MemberNode[] {
    const healthyOnly = opts.healthyOnly ?? true;
    return this.members.all().filter((node) => {
      if (healthyOnly && node.health !== 'healthy') return false;
      return Object.entries(filter).every(([key, value]) =>
        this.matchAttr(node, key, value),
      );
    });
  }

  /**
   * 路由查询：返回满足筛选条件、健康且权重最高的节点。
   * 无候选时返回 null。
   */
  route(filter: AttributeFilter): MemberNode | null {
    const candidates = this.select(filter);
    if (candidates.length === 0) return null;
    return candidates.reduce((best, node) => {
      if (node.record.meta.weight !== best.record.meta.weight) {
        return node.record.meta.weight > best.record.meta.weight ? node : best;
      }
      return node.id < best.id ? node : best;
    });
  }

  private matchAttr(node: MemberNode, key: string, value: string): boolean {
    const meta = node.record.meta;
    if (key.startsWith('labels.')) {
      return meta.labels[key.slice('labels.'.length)] === value;
    }
    switch (key) {
      case 'zone':
        return meta.zone === value;
      case 'role':
        return meta.role === value;
      case 'weight':
        return String(meta.weight) === value;
      case 'id':
        return node.id === value;
      default:
        return meta.labels[key] === value;
    }
  }
}
