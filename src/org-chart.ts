/**
 * 可追溯员工组织架构时态树。
 *
 * 汇报线（manager）与部门归属（department）均为双时态数据：
 * 任意“系统知悉时刻 × 业务时刻”都可还原当时的组织形态，
 * 并支持沿汇报线向上还原完整汇报链。
 */
import { BitemporalTable, type BitemporalRecord } from './bitemporal.ts';

export interface ChainNode {
  employeeId: string;
  managerId: string | null;
  departmentId: string | null;
}

export class OrgChart {
  private readonly managers = new BitemporalTable<string, string | null>();
  private readonly departments = new BitemporalTable<string, string>();

  /** 录入汇报线：employee 在业务区间 [validFrom, validTo) 内向 manager 汇报（null 表示无上级/根节点）。 */
  setManager(employeeId: string, managerId: string | null, validFrom: number, validTo: number, sysFrom: number): void {
    if (employeeId === managerId) {
      throw new RangeError(`员工 ${employeeId} 不能向自己汇报`);
    }
    this.managers.insert(employeeId, managerId, validFrom, validTo, sysFrom);
  }

  /** 录入部门归属：employee 在业务区间 [validFrom, validTo) 内归属 department。 */
  setDepartment(employeeId: string, departmentId: string, validFrom: number, validTo: number, sysFrom: number): void {
    this.departments.insert(employeeId, departmentId, validFrom, validTo, sysFrom);
  }

  /** 在系统知悉时刻 sysTime 看来，业务时刻 validTime 时 employee 的直接上级；undefined 表示无记录。 */
  managerAt(employeeId: string, validTime: number, sysTime: number): string | null | undefined {
    return this.managers.valueAt(employeeId, validTime, sysTime);
  }

  /** 在系统知悉时刻 sysTime 看来，业务时刻 validTime 时 employee 的部门；undefined 表示无记录。 */
  departmentAt(employeeId: string, validTime: number, sysTime: number): string | undefined {
    return this.departments.valueAt(employeeId, validTime, sysTime);
  }

  /**
   * 还原汇报链：从 employee 出发沿直接上级逐级向上，直到根节点。
   * 返回自员工本人起、至最高上级止的有序链（含环检测，防止脏数据死循环）。
   */
  reportingChainAt(employeeId: string, validTime: number, sysTime: number): ChainNode[] {
    const chain: ChainNode[] = [];
    const visited = new Set<string>();
    let current: string | null | undefined = employeeId;

    while (current != null && !visited.has(current)) {
      visited.add(current);
      const managerId = this.managerAt(current, validTime, sysTime);
      const departmentId = this.departmentAt(current, validTime, sysTime) ?? null;
      chain.push({ employeeId: current, managerId: managerId ?? null, departmentId });
      current = managerId;
    }
    return chain;
  }

  /** 汇报线的完整审计轨迹（含已被修订关闭的记录）。 */
  managerHistory(employeeId: string): BitemporalRecord<string | null>[] {
    return this.managers.history(employeeId);
  }

  /** 部门归属的完整审计轨迹。 */
  departmentHistory(employeeId: string): BitemporalRecord<string>[] {
    return this.departments.history(employeeId);
  }

  /** 系统知悉时刻 sysTime 下，employee 汇报线的“当时生效”业务时间线。 */
  managerTimelineAt(employeeId: string, sysTime: number): BitemporalRecord<string | null>[] {
    return this.managers.timelineAt(employeeId, sysTime);
  }
}
