import type { Problem } from './types.ts';

/**
 * 内置样例：14 台设备、4 类备件。
 * 库存只够修少数设备，必须权衡“拆谁补谁”：
 * 拆解会损失残值，但回收的备件（有 20% 损耗）能让高价值设备齐套。
 */
export const sampleProblem: Problem = {
  budget: 60,
  recoveryYield: 0.8,
  stock: { 液压泵: 1, 控制板: 1, 密封件: 4, 轴承: 2 },
  devices: [
    { id: 'D01', repairCost: 18, repairValue: 60, residualValue: 12,
      repairParts: { 液压泵: 1, 密封件: 2 }, salvageParts: { 轴承: 2 } },
    { id: 'D02', repairCost: 22, repairValue: 75, residualValue: 15,
      repairParts: { 控制板: 1, 轴承: 2 }, salvageParts: { 密封件: 2 } },
    { id: 'D03', repairCost: 15, repairValue: 48, residualValue: 10,
      repairParts: { 液压泵: 1, 控制板: 1 }, salvageParts: { 密封件: 1, 轴承: 1 } },
    { id: 'D04', repairCost: 12, repairValue: 40, residualValue: 8,
      repairParts: { 密封件: 3 }, salvageParts: { 液压泵: 1 } },
    { id: 'D05', repairCost: 20, repairValue: 55, residualValue: 14,
      repairParts: { 控制板: 2 }, salvageParts: { 液压泵: 1, 密封件: 1 } },
    { id: 'D06', repairCost: 10, repairValue: 30, residualValue: 6,
      repairParts: { 轴承: 2, 密封件: 1 }, salvageParts: { 控制板: 1 } },
    { id: 'D07', repairCost: 8, repairValue: 22, residualValue: 5,
      repairParts: { 密封件: 2 }, salvageParts: { 轴承: 1 } },
    { id: 'D08', repairCost: 14, repairValue: 38, residualValue: 9,
      repairParts: { 液压泵: 1, 轴承: 1 }, salvageParts: { 控制板: 1, 密封件: 1 } },
    { id: 'D09', repairCost: 9, repairValue: 15, residualValue: 4,
      repairParts: { 控制板: 1, 密封件: 1 }, salvageParts: { 液压泵: 2, 轴承: 1 } },
    { id: 'D10', repairCost: 11, repairValue: 18, residualValue: 5,
      repairParts: { 液压泵: 1, 密封件: 2 }, salvageParts: { 控制板: 2 } },
    { id: 'D11', repairCost: 7, repairValue: 12, residualValue: 3,
      repairParts: { 轴承: 1 }, salvageParts: { 密封件: 3, 液压泵: 1 } },
    { id: 'D12', repairCost: 13, repairValue: 20, residualValue: 6,
      repairParts: { 控制板: 1, 轴承: 1 }, salvageParts: { 密封件: 2, 轴承: 2 } },
    { id: 'D13', repairCost: 6, repairValue: 9, residualValue: 2,
      repairParts: { 密封件: 1 }, salvageParts: { 控制板: 1, 轴承: 1 } },
    { id: 'D14', repairCost: 16, repairValue: 26, residualValue: 7,
      repairParts: { 液压泵: 1, 控制板: 1, 密封件: 1 }, salvageParts: { 轴承: 2, 密封件: 1 } },
  ],
};
