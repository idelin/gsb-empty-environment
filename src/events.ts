export type StepEventType =
  | 'run_started'
  | 'step_completed'
  | 'step_failed'
  | 'compensation_started'
  | 'step_compensated'
  | 'compensation_failed'
  | 'workflow_completed'
  | 'workflow_failed';

export interface StepEvent {
  seq: number;
  at: string;
  workflowId: string;
  type: StepEventType;
  stepId?: string;
  detail?: string;
}

export type NewEvent = Pick<StepEvent, 'type'> & Partial<Pick<StepEvent, 'stepId' | 'detail'>>;
