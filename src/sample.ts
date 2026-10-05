import { Scenario } from "./scheduler";

/** 内置示例: 2 种型号 / 3 台设备 / 1 个维护工位 / 15 单 */
export const sampleScenario: Scenario = {
  stationCount: 1,
  models: [
    { name: "excavator", units: 2, threshold: 10, maintenanceDuration: 4 },
    { name: "crane", units: 1, threshold: 8, maintenanceDuration: 3 },
  ],
  orders: [
    { id: "O1", model: "excavator", start: 0, end: 6, penalty: 50 },
    { id: "O2", model: "excavator", start: 2, end: 8, penalty: 45 },
    { id: "O3", model: "excavator", start: 8, end: 14, penalty: 60 },
    { id: "O4", model: "excavator", start: 10, end: 16, penalty: 55 },
    { id: "O5", model: "excavator", start: 16, end: 22, penalty: 70 },
    { id: "O6", model: "excavator", start: 18, end: 24, penalty: 65 },
    { id: "O7", model: "crane", start: 0, end: 5, penalty: 30 },
    { id: "O8", model: "crane", start: 6, end: 10, penalty: 35 },
    { id: "O9", model: "crane", start: 11, end: 15, penalty: 40 },
    { id: "O10", model: "crane", start: 16, end: 20, penalty: 25 },
    { id: "O11", model: "excavator", start: 24, end: 30, penalty: 80 },
    { id: "O12", model: "excavator", start: 25, end: 31, penalty: 75 },
    { id: "O13", model: "crane", start: 21, end: 27, penalty: 50 },
    { id: "O14", model: "excavator", start: 31, end: 35, penalty: 20 },
    { id: "O15", model: "crane", start: 28, end: 32, penalty: 30 },
  ],
};
