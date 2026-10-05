export interface DelayStep {
  id: string;
  type: 'delay';
  ms: number;
  dependsOn?: string[];
}

export interface DropStep {
  id: string;
  type: 'drop';
  from: string;
  to: string;
  count: number;
  dependsOn?: string[];
}

export interface RestartStep {
  id: string;
  type: 'restart';
  node: string;
  dependsOn?: string[];
}

export type Step = DelayStep | DropStep | RestartStep;

export interface Script {
  nodes: string[];
  steps: Step[];
}

export function removeStep(script: Script, stepId: string): Script {
  return {
    nodes: [...script.nodes],
    steps: script.steps.filter((step) => step.id !== stepId),
  };
}
