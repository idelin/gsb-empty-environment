export declare const DAY_MINUTES = 1440;
export declare class ScheduleInputError extends Error {
    readonly code = "INVALID_SCHEDULE_INPUT";
    readonly details?: unknown;
    constructor(message: string, details?: unknown);
}
export declare class ScheduleInfeasibleError extends Error {
    readonly code = "SCHEDULE_INFEASIBLE";
    readonly reason: string;
    readonly details?: unknown;
    constructor(reason: string, message: string, details?: unknown);
}
export type InstantInput = number | string | Date;
export type FractionInput = number | {
    numerator: number;
    denominator: number;
} | {
    num: number;
    den: number;
} | [number, number] | string;
export interface FreezeWindowInput {
    start: string | number;
    end: string | number;
    from?: string | number;
    to?: string | number;
    begin?: string | number;
    finish?: string | number;
    endMinute?: string | number;
    startMinute?: string | number;
    overnight?: boolean;
    crossesMidnight?: boolean;
    allDay?: boolean;
}
export interface UtcOffsetIntervalInput {
    startUtc?: InstantInput;
    endUtc?: InstantInput;
    start?: InstantInput;
    end?: InstantInput;
    from?: InstantInput;
    to?: InstantInput;
    beginsAt?: InstantInput;
    endsAt?: InstantInput;
    offsetMinutes?: number | string;
    utcOffsetMinutes?: number | string;
    offset?: number | string;
}
export interface RegionInput {
    id?: string;
    regionId?: string;
    freezeWindows?: FreezeWindowInput[];
    windows?: FreezeWindowInput[];
    dailyFreezeWindows?: FreezeWindowInput[];
    utcOffsetMinutes?: number | string;
    defaultUtcOffsetMinutes?: number | string;
    utcOffset?: number | string;
    utcOffsetIntervals?: UtcOffsetIntervalInput[];
    offsetIntervals?: UtcOffsetIntervalInput[];
    utcOffsets?: UtcOffsetIntervalInput[];
    offsets?: UtcOffsetIntervalInput[];
    M?: number;
    m?: number;
    maxConcurrent?: number;
    maxConcurrentUpgrades?: number;
}
export interface CellInput {
    id?: string | number;
    name?: string;
    regionId?: string;
    region?: string | RegionInput;
    trafficWeight?: number;
    weight?: number;
    traffic?: number;
    upgradeMinutes?: number;
    upgradeDurationMinutes?: number;
    upgrade?: number;
    bakeMinutes?: number;
    healthObservationMinutes?: number;
    observationMinutes?: number;
    bake?: number;
    durationMinutes?: number;
    occupancyMinutes?: number;
}
export type EdgeInput = [string | number, string | number] | {
    a: string | number;
    b: string | number;
} | {
    from: string | number;
    to: string | number;
} | {
    cellA: string | number;
    cellB: string | number;
};
export interface ScheduleInput {
    cells: CellInput[];
    regions?: Array<RegionInput & {
        id?: string;
    }> | Record<string, RegionInput>;
    startUtc?: InstantInput;
    startTime?: InstantInput;
    globalStart?: InstantInput;
    start?: InstantInput;
    U?: FractionInput;
    u?: FractionInput;
    maxUnavailableFraction?: FractionInput;
    maxUnavailableTrafficFraction?: FractionInput;
    globalMaxUnavailableFraction?: FractionInput;
    maxUnavailableWeight?: number;
    globalTrafficLimit?: number;
    totalTrafficWeight?: number;
    totalWeight?: number;
    M?: number | Record<string, number>;
    m?: number | Record<string, number>;
    maxConcurrentPerRegion?: number | Record<string, number>;
    regionConcurrency?: number | Record<string, number>;
    disasterRecoveryEdges?: EdgeInput[];
    drEdges?: EdgeInput[];
    mutualBackupEdges?: EdgeInput[];
    edges?: EdgeInput[];
}
export interface ScheduledCell {
    id: string;
    index: number;
    regionId: string;
    startMinute: number;
    endMinute: number;
    startUtc: string;
    endUtc: string;
    upgradeMinutes: number;
    bakeMinutes: number;
    occupancyMinutes: number;
    trafficWeight: number;
}
export interface ScheduleResult {
    feasible: true;
    starts: number[];
    startMinutes: number[];
    startVector: number[];
    ends: number[];
    endMinutes: number[];
    makespanMinutes: number;
    sumStartMinutes: number;
    lexKey: number[];
    cells: ScheduledCell[];
}
export interface Violation {
    code: string;
    message: string;
    cellIds?: string[];
    minute?: number;
}
export interface ValidationResult {
    valid: boolean;
    violations: Violation[];
}
interface NormalizedWindow {
    startPhase: number;
    endAnchor: number;
    length: number;
}
interface OffsetSegment {
    start: number;
    end: number;
    offset: number;
}
interface NormalizedRegion {
    id: string;
    input: RegionInput;
    windows: NormalizedWindow[];
    offsets: OffsetSegment[];
    maxGapMinutes: number;
    maxConcurrent: number;
}
interface NormalizedCell {
    index: number;
    id: string;
    regionId: string;
    weight: number;
    upgradeMinutes: number;
    bakeMinutes: number;
    duration: number;
}
type GlobalLimit = {
    mode: "fraction";
    numerator: number;
    denominator: number;
    totalWeight: number;
} | {
    mode: "number";
    value: number;
    totalWeight: number;
} | {
    mode: "absolute";
    value: number;
};
interface ScheduleContext {
    input: ScheduleInput;
    base: number;
    cells: NormalizedCell[];
    regions: Map<string, NormalizedRegion>;
    drMasks: number[];
    drEdges: Array<[number, number]>;
    globalLimit: GlobalLimit;
}
export declare function parseInstantToMinute(value: InstantInput | undefined): number;
export declare class FreezeCalendar {
    readonly windows: NormalizedWindow[];
    readonly offsets: OffsetSegment[];
    readonly maxGapMinutes: number;
    constructor(windows: NormalizedWindow[], offsets: OffsetSegment[]);
    segmentAt(instant: number): OffsetSegment;
    isAllowed(start: number, end: number): boolean;
    firstBlockingBoundary(start: number, end: number): number | null;
    earliestAllowedStart(lower: number, duration: number, cellId?: string): number;
}
export declare function createScheduleContext(input: ScheduleInput): ScheduleContext;
export declare function scheduleUpgrades(input: ScheduleInput): ScheduleResult;
export declare function tryScheduleUpgrades(input: ScheduleInput): {
    success: true;
    result: ScheduleResult;
} | {
    success: false;
    error: ScheduleInfeasibleError | ScheduleInputError;
};
export declare function validateSchedule(input: ScheduleInput, starts: number[] | ScheduleResult, options?: {
    absoluteNumbers?: boolean;
}): ValidationResult;
export declare function assertValidSchedule(input: ScheduleInput, result: ScheduleResult): void;
export declare const schedule: typeof scheduleUpgrades;
export declare const planUpgrades: typeof scheduleUpgrades;
export declare const solveSchedule: typeof scheduleUpgrades;
export declare const optimizeSchedule: typeof scheduleUpgrades;
export declare const verifySchedule: typeof validateSchedule;
export default scheduleUpgrades;
