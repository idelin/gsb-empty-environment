export type NodeId = string;

/**
 * 配置状态机：
 *   stable(old) --enter-joint--> joint(old, new) --leave-joint--> stable(new)
 * 不允许 stable -> stable 的直接跳变（即不允许跳过中间联合确认阶段）。
 */
export type ConfigState =
  | { readonly kind: "stable"; readonly members: readonly NodeId[] }
  | {
      readonly kind: "joint";
      readonly oldMembers: readonly NodeId[];
      readonly newMembers: readonly NodeId[];
    };

export type ConfigCommand =
  | { readonly kind: "enter-joint"; readonly newMembers: readonly NodeId[] }
  | { readonly kind: "leave-joint" }
  // 仅供反例使用：绕过联合共识的“直接切换”，正常提案路径会拒绝它。
  | { readonly kind: "switch-direct"; readonly newMembers: readonly NodeId[] };

export function majorityThreshold(size: number): number {
  return Math.floor(size / 2) + 1;
}

export function isMajority(
  group: readonly NodeId[],
  holders: ReadonlySet<NodeId>,
): boolean {
  let count = 0;
  for (const id of group) {
    if (holders.has(id)) count += 1;
  }
  return count >= majorityThreshold(group.length);
}

/**
 * 提交判定所需的多数派分组：
 * - 稳定期：唯一一组（当前成员）；
 * - 联合期：旧组与新组两组，各自必须独立达到多数。
 */
export function quorumGroups(
  config: ConfigState,
): readonly (readonly NodeId[])[] {
  return config.kind === "stable"
    ? [config.members]
    : [config.oldMembers, config.newMembers];
}

/** 提交判定：entry 的持有者集合必须在配置要求的每一个分组中都达到多数。 */
export function quorumReached(
  config: ConfigState,
  holders: ReadonlySet<NodeId>,
): boolean {
  return quorumGroups(config).every((group) => isMajority(group, holders));
}

/**
 * 配置状态机迁移。非法迁移（跳步、重复进入联合期等）直接抛错。
 * 配置项本身也是日志条目，只有按所在阶段的提交规则提交后，
 * 才会通过该函数推进“已提交配置”。
 */
export function applyConfigCommand(
  state: ConfigState,
  command: ConfigCommand,
): ConfigState {
  switch (command.kind) {
    case "enter-joint":
      if (state.kind !== "stable") {
        throw new Error(
          "非法配置迁移：enter-joint 只能在 stable 状态下发起（不能重复进入联合期）",
        );
      }
      return {
        kind: "joint",
        oldMembers: state.members,
        newMembers: [...command.newMembers],
      };
    case "leave-joint":
      if (state.kind !== "joint") {
        throw new Error(
          "非法配置迁移：leave-joint 只能在 joint 状态下发起（不能跳过中间确认直达新配置）",
        );
      }
      return { kind: "stable", members: [...state.newMembers] };
    case "switch-direct":
      // 错误实现专用路径：从旧配置直接跳到新配置，不经过联合确认。
      if (state.kind !== "stable") {
        throw new Error("非法配置迁移：switch-direct 只能在 stable 状态下发起");
      }
      return { kind: "stable", members: [...command.newMembers] };
  }
}
