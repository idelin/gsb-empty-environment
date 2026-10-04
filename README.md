# 审批指派求解器

为「发起 / 复核 / 付款」三个角色求解人员指派，满足：

- **职责分离**：三个角色必须由不同责任主体承担；
- **代理等价**：代理关系（可传递）归并为同一责任主体，回避与职责分离均按主体判定；
- **回避规则**：岗位禁任（某主体不得担任某角色）与双人冲突（两主体不得同时担任指定角色）。

不可行时输出**最小冲突约束集**（放宽后即可恢复可行的最小回避规则集合），
或指出结构性不可行（如候选责任主体不足 3 个）。
求解轨迹最多 15 步，每步记录候选人被排除的具体原因。

## 使用

```bash
npm install -D typescript && npx tsc   # 编译到 dist/
node dist/approval-assignment-solver.js  # 运行内置示例（场景 A/B/C）
```

或在代码中调用：

```ts
import { solveApprovalAssignment, formatResult, SolverInput } from './approval-assignment-solver';

const input: SolverInput = {
  candidates: {
    initiate: ['张三', '李四'],
    review: ['王五', '陈九'],
    pay: ['赵六', '王五'],
  },
  proxies: [['陈九', '张三']],           // 陈九是张三的代理人
  avoidances: [
    { kind: 'role-ban', id: 'R1', person: '赵六', role: 'pay', reason: '利益申报，不得经手付款' },
    { kind: 'pair-conflict', id: 'R2', personA: '王五', roleA: 'review', personB: '赵六', roleB: 'pay', reason: '直系亲属回避' },
  ],
};

console.log(formatResult(solveApprovalAssignment(input)));
```

## 输出

- `feasible` + `assignment`：可行指派；
- `minimalRelaxations`：最小冲突约束集（可能多组，放宽任一组即恢复可行）及 `relaxedAssignment`；
- `structuralReason`：移除全部回避规则仍不可行时的结构性原因；
- `steps`：不超过 15 步的求解轨迹，每步含 `exclusions`（候选人 + 排除原因）。
