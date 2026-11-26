export const DAY_MINUTES = 1440;
export class ScheduleInputError extends Error {
    code = "INVALID_SCHEDULE_INPUT";
    details;
    constructor(message, details) {
        super(message);
        this.name = "ScheduleInputError";
        this.details = details;
    }
}
export class ScheduleInfeasibleError extends Error {
    code = "SCHEDULE_INFEASIBLE";
    reason;
    details;
    constructor(reason, message, details) {
        super(message);
        this.name = "ScheduleInfeasibleError";
        this.reason = reason;
        this.details = details;
    }
}
export function parseInstantToMinute(value) {
    if (value === undefined)
        return 0;
    if (value instanceof Date) {
        if (Number.isNaN(value.getTime())) {
            throw new ScheduleInputError("Invalid Date instant.");
        }
        return Math.floor(value.getTime() / 60000);
    }
    if (typeof value === "number") {
        if (!Number.isFinite(value)) {
            throw new ScheduleInputError(`Invalid numeric instant: ${value}`);
        }
        if (Math.abs(value) >= 1_000_000_000_000) {
            return Math.floor(value / 60000);
        }
        return Math.floor(value);
    }
    if (typeof value === "string") {
        const text = value.trim();
        if (/^-?\d+$/.test(text))
            return Number(text);
        const milliseconds = Date.parse(text);
        if (!Number.isNaN(milliseconds))
            return Math.floor(milliseconds / 60000);
    }
    throw new ScheduleInputError(`Invalid UTC instant: ${String(value)}`);
}
function parseDayMinute(value, field) {
    if (value === undefined) {
        throw new ScheduleInputError(`Missing ${field}.`);
    }
    if (typeof value === "number") {
        if (!Number.isFinite(value) || value < 0 || value > DAY_MINUTES) {
            throw new ScheduleInputError(`Invalid minute-of-day ${field}: ${value}`);
        }
        return Math.floor(value);
    }
    const match = /^(\d{1,2}):(\d{2})(?::(\d{2}))?$/.exec(value.trim());
    if (!match) {
        throw new ScheduleInputError(`Invalid HH:mm time ${field}: ${value}`);
    }
    const hours = Number(match[1]);
    const minutes = Number(match[2]);
    if (hours > 24 || minutes > 59 || (hours === 24 && minutes !== 0)) {
        throw new ScheduleInputError(`Invalid clock time ${field}: ${value}`);
    }
    return hours * 60 + minutes;
}
function parseOffsetMinutes(value) {
    if (typeof value === "number") {
        if (!Number.isInteger(value))
            throw new ScheduleInputError("UTC offset must be an integer minute count.");
        return value;
    }
    if (typeof value !== "string") {
        throw new ScheduleInputError("Invalid UTC offset.");
    }
    const text = value.trim().toUpperCase().replace(/^UTC/, "");
    const numeric = /^([+-]?\d+)$/.exec(text);
    if (numeric)
        return Number(numeric[1]);
    const clock = /^([+-]?)(\d{1,3}):(\d{2})$/.exec(text);
    if (clock) {
        const sign = clock[1] === "-" ? -1 : 1;
        return sign * (Number(clock[2]) * 60 + Number(clock[3]));
    }
    throw new ScheduleInputError(`Invalid UTC offset: ${value}`);
}
function asPositiveInt(value, field, options = {}) {
    if (typeof value !== "number" || !Number.isFinite(value) || !Number.isInteger(value)) {
        throw new ScheduleInputError(`${field} must be an integer.`);
    }
    if (options.allowZero ? value < 0 : value <= 0) {
        throw new ScheduleInputError(`${field} must be ${options.allowZero ? "non-negative" : "positive"}.`);
    }
    return value;
}
function modDay(value) {
    return ((value % DAY_MINUTES) + DAY_MINUTES) % DAY_MINUTES;
}
function normalizeWindow(input) {
    const startValue = input.start ?? input.startMinute ?? input.from ?? input.begin;
    const endValue = input.end ?? input.endMinute ?? input.to ?? input.finish;
    const startPhase = parseDayMinute(startValue, "freeze window start");
    const endMinute = parseDayMinute(endValue, "freeze window end");
    let endAnchor;
    let length;
    if (input.overnight === true || input.crossesMidnight === true) {
        if (endMinute === 1440 && startPhase < endMinute) {
            endAnchor = endMinute;
            length = endMinute - startPhase;
        }
        else {
            endAnchor = DAY_MINUTES + endMinute;
            length = DAY_MINUTES - startPhase + endMinute;
        }
    }
    else if (endMinute <= startPhase) {
        endAnchor = DAY_MINUTES + endMinute;
        length = DAY_MINUTES - startPhase + endMinute;
    }
    else {
        endAnchor = endMinute;
        length = endMinute - startPhase;
    }
    if (input.allDay === true) {
        length = DAY_MINUTES;
        endAnchor = startPhase + DAY_MINUTES;
    }
    if (length <= 0 || length > DAY_MINUTES) {
        throw new ScheduleInputError("Freeze window must have a positive length no greater than one day.");
    }
    return { startPhase, endAnchor, length };
}
function mergeGaps(windows) {
    if (windows.length === 0)
        return Number.POSITIVE_INFINITY;
    const intervals = [];
    for (let day = -2; day <= 2; day += 1) {
        for (const window of windows) {
            const start = day * DAY_MINUTES + window.startPhase;
            const end = day * DAY_MINUTES + window.endAnchor;
            intervals.push([start, end]);
        }
    }
    intervals.sort((a, b) => a[0] - b[0] || a[1] - b[1]);
    const merged = [];
    for (const interval of intervals) {
        const last = merged[merged.length - 1];
        if (!last || interval[0] > last[1]) {
            merged.push([interval[0], interval[1]]);
        }
        else {
            last[1] = Math.max(last[1], interval[1]);
        }
    }
    let maxGap = 0;
    for (let i = 1; i < merged.length; i += 1) {
        maxGap = Math.max(maxGap, merged[i][0] - merged[i - 1][1]);
    }
    return maxGap;
}
function normalizeOffsets(region) {
    const rawIntervals = region.utcOffsetIntervals ?? region.offsetIntervals ?? region.utcOffsets ?? region.offsets ?? [];
    const defaultOffset = parseOffsetMinutes(region.utcOffsetMinutes ?? region.defaultUtcOffsetMinutes ?? region.utcOffset ?? 0);
    const parsed = rawIntervals.map((interval, index) => {
        const offset = parseOffsetMinutes(interval.offsetMinutes ?? interval.utcOffsetMinutes ?? interval.offset ?? defaultOffset);
        const startRaw = interval.startUtc ?? interval.start ?? interval.from ?? interval.beginsAt ?? undefined;
        const endRaw = interval.endUtc ?? interval.end ?? interval.to ?? interval.endsAt ?? undefined;
        return {
            start: startRaw === undefined ? Number.NEGATIVE_INFINITY : parseInstantToMinute(startRaw),
            end: endRaw === undefined ? Number.POSITIVE_INFINITY : parseInstantToMinute(endRaw),
            offset,
        };
    });
    parsed.sort((a, b) => a.start - b.start || a.end - b.end);
    for (let i = 0; i < parsed.length; i += 1) {
        if (parsed[i].start >= parsed[i].end) {
            throw new ScheduleInputError("UTC offset interval start must be before end.");
        }
        if (i > 0 && parsed[i].start < parsed[i - 1].end) {
            throw new ScheduleInputError("UTC offset intervals overlap.");
        }
    }
    const complete = [];
    let cursor = Number.NEGATIVE_INFINITY;
    for (const interval of parsed) {
        if (interval.start > cursor) {
            complete.push({ start: cursor, end: interval.start, offset: defaultOffset });
        }
        complete.push({
            start: Number.isFinite(interval.start) ? Math.max(cursor, interval.start) : cursor,
            end: interval.end,
            offset: interval.offset,
        });
        cursor = interval.end;
    }
    complete.push({ start: cursor, end: Number.POSITIVE_INFINITY, offset: defaultOffset });
    const merged = [];
    for (const segment of complete) {
        const last = merged[merged.length - 1];
        if (last && last.end === segment.start && last.offset === segment.offset) {
            last.end = segment.end;
        }
        else if (last && segment.start === segment.end) {
            continue;
        }
        else {
            merged.push({ ...segment });
        }
    }
    return merged;
}
function findWindowOverlap(window, localStart, localEnd) {
    let day = Math.floor((localStart - window.endAnchor) / DAY_MINUTES) + 1;
    let end = day * DAY_MINUTES + window.endAnchor;
    while (end <= localStart) {
        day += 1;
        end = day * DAY_MINUTES + window.endAnchor;
    }
    const start = end - window.length;
    if (start < localEnd && end > localStart)
        return { start, end };
    return null;
}
export class FreezeCalendar {
    windows;
    offsets;
    maxGapMinutes;
    constructor(windows, offsets) {
        this.windows = windows;
        this.offsets = offsets;
        this.maxGapMinutes = mergeGaps(windows);
    }
    segmentAt(instant) {
        for (const segment of this.offsets) {
            if (segment.start <= instant && instant < segment.end)
                return segment;
        }
        return this.offsets[this.offsets.length - 1];
    }
    isAllowed(start, end) {
        if (!Number.isFinite(start) || !Number.isFinite(end) || end <= start)
            return false;
        for (const segment of this.offsets) {
            if (segment.end <= start)
                continue;
            if (segment.start >= end)
                break;
            const intervalStart = Math.max(start, segment.start);
            const intervalEnd = Math.min(end, segment.end);
            if (intervalStart >= intervalEnd)
                continue;
            const localStart = intervalStart + segment.offset;
            const localEnd = intervalEnd + segment.offset;
            for (const window of this.windows) {
                if (findWindowOverlap(window, localStart, localEnd))
                    return false;
            }
        }
        return true;
    }
    firstBlockingBoundary(start, end) {
        for (const segment of this.offsets) {
            if (segment.end <= start)
                continue;
            if (segment.start >= end)
                break;
            const intervalStart = Math.max(start, segment.start);
            const intervalEnd = Math.min(end, segment.end);
            if (intervalStart >= intervalEnd)
                continue;
            const localStart = intervalStart + segment.offset;
            const localEnd = intervalEnd + segment.offset;
            let boundary = Number.POSITIVE_INFINITY;
            for (const window of this.windows) {
                const overlap = findWindowOverlap(window, localStart, localEnd);
                if (!overlap)
                    continue;
                const windowEndUtc = overlap.end - segment.offset;
                boundary = Math.min(boundary, segment.end, windowEndUtc);
            }
            if (Number.isFinite(boundary))
                return boundary;
        }
        return null;
    }
    earliestAllowedStart(lower, duration, cellId) {
        let current = lower;
        const seenTailPhases = new Set();
        for (let iteration = 0; iteration < 5_000_000; iteration += 1) {
            if (this.isAllowed(current, current + duration))
                return current;
            const segment = this.segmentAt(current);
            if (segment.end === Number.POSITIVE_INFINITY) {
                const phase = modDay(current);
                if (seenTailPhases.has(phase)) {
                    throw new ScheduleInfeasibleError("CELL_NEVER_FITS_FREEZE_WINDOWS", `细胞 ${cellId ?? ""} 的耗时 ${duration} 分钟超过了区域冻结窗之间可周期性获得的最大间隙，无法排期。`, { cellId, durationMinutes: duration, lowerMinute: lower });
                }
                seenTailPhases.add(phase);
            }
            const boundary = this.firstBlockingBoundary(current, current + duration);
            if (boundary === null) {
                throw new ScheduleInfeasibleError("FREEZE_CALENDAR_ERROR", "无法计算下一个冻结窗结束时刻。", { cellId, current, duration });
            }
            current = boundary > current ? boundary : current + 1;
        }
        throw new ScheduleInfeasibleError("FREEZE_SEARCH_LIMIT", "寻找合法冻结窗间隙时超过安全迭代上限。", { cellId, lower, duration });
    }
}
function normalizeFraction(input) {
    if (input === undefined) {
        return { mode: "number", value: 1, totalWeight: Number.NaN };
    }
    if (typeof input === "number") {
        if (!Number.isFinite(input) || input < 0) {
            throw new ScheduleInputError("U must be a fraction in [0, 1].");
        }
        if (input <= 1) {
            return { mode: "number", value: input, totalWeight: Number.NaN };
        }
        if (input <= 100) {
            return { mode: "number", value: input / 100, totalWeight: Number.NaN };
        }
        throw new ScheduleInputError("U must be a fraction in [0, 1] or a percentage in [0, 100].");
    }
    if (Array.isArray(input)) {
        const [numerator, denominator] = input;
        if (!Number.isInteger(numerator) || !Number.isInteger(denominator) || denominator <= 0 || numerator < 0) {
            throw new ScheduleInputError("Invalid rational U.");
        }
        return { mode: "fraction", numerator, denominator, totalWeight: Number.NaN };
    }
    if (typeof input === "string") {
        const percentage = /^(\d+(?:\.\d+)?)\s*%$/.exec(input.trim());
        if (percentage) {
            return { mode: "number", value: Number(percentage[1]) / 100, totalWeight: Number.NaN };
        }
        const slash = /^(\d+)\s*\/\s*(\d+)$/.exec(input.trim());
        if (slash) {
            return {
                mode: "fraction",
                numerator: Number(slash[1]),
                denominator: Number(slash[2]),
                totalWeight: Number.NaN,
            };
        }
        const decimal = Number(input);
        if (Number.isFinite(decimal) && decimal >= 0 && decimal <= 1) {
            return { mode: "number", value: decimal, totalWeight: Number.NaN };
        }
        throw new ScheduleInputError(`Invalid U fraction: ${input}`);
    }
    const fraction = input;
    const numerator = fraction.numerator ?? fraction.num;
    const denominator = fraction.denominator ?? fraction.den;
    if (numerator === undefined ||
        denominator === undefined ||
        !Number.isInteger(numerator) ||
        !Number.isInteger(denominator) ||
        denominator <= 0 ||
        numerator < 0) {
        throw new ScheduleInputError("Invalid rational U.");
    }
    return { mode: "fraction", numerator, denominator, totalWeight: Number.NaN };
}
function buildRegionMap(input, base) {
    const rawRegions = new Map();
    const addRawRegion = (id, region) => {
        const existing = rawRegions.get(id);
        rawRegions.set(id, { ...(existing ?? {}), ...(region ?? {}), id });
    };
    if (Array.isArray(input.regions)) {
        for (const region of input.regions) {
            const id = region.id ?? region.regionId;
            if (!id)
                throw new ScheduleInputError("Every region must have an id.");
            addRawRegion(id, region);
        }
    }
    else if (input.regions && typeof input.regions === "object") {
        for (const [id, region] of Object.entries(input.regions)) {
            addRawRegion(id, { ...region, id: region.id ?? id });
        }
    }
    input.cells.forEach((cell, index) => {
        const fallbackId = cell.id === undefined ? `cell-${index}` : String(cell.id);
        let regionId = cell.regionId;
        if (typeof cell.region === "string")
            regionId = cell.region;
        if (cell.region && typeof cell.region === "object") {
            const regionObject = cell.region;
            regionId = regionObject.id ?? regionObject.regionId ?? regionId ?? `${fallbackId}-region`;
            addRawRegion(regionId, regionObject);
        }
        if (!regionId)
            regionId = "default";
        if (!rawRegions.has(regionId))
            addRawRegion(regionId, { id: regionId });
    });
    const concurrencyOption = input.M ?? input.m ?? input.maxConcurrentPerRegion ?? input.regionConcurrency;
    let defaultConcurrency;
    const concurrencyById = new Map();
    if (typeof concurrencyOption === "number") {
        defaultConcurrency = asPositiveInt(concurrencyOption, "M", { allowZero: true });
    }
    else if (concurrencyOption && typeof concurrencyOption === "object") {
        for (const [id, value] of Object.entries(concurrencyOption)) {
            concurrencyById.set(id, asPositiveInt(value, `M[${id}]`, { allowZero: true }));
        }
    }
    const result = new Map();
    for (const [id, raw] of rawRegions) {
        const windowInputs = raw.freezeWindows ?? raw.windows ?? raw.dailyFreezeWindows ?? [];
        const windows = windowInputs.map(normalizeWindow);
        const offsets = normalizeOffsets(raw);
        const maxConcurrent = concurrencyById.get(id) ??
            (typeof raw.M === "number" ? raw.M : undefined) ??
            (typeof raw.m === "number" ? raw.m : undefined) ??
            raw.maxConcurrentUpgrades ??
            raw.maxConcurrent ??
            defaultConcurrency ??
            Number.MAX_SAFE_INTEGER;
        if (!Number.isInteger(maxConcurrent) || maxConcurrent < 0) {
            throw new ScheduleInputError(`Invalid max concurrency for region ${id}.`);
        }
        result.set(id, {
            id,
            input: raw,
            windows,
            offsets,
            maxGapMinutes: mergeGaps(windows),
            maxConcurrent,
        });
    }
    return result;
}
function normalizeCells(input, regions) {
    if (!Array.isArray(input.cells) || input.cells.length === 0) {
        throw new ScheduleInputError("cells must be a non-empty array.");
    }
    if (input.cells.length > 31) {
        throw new ScheduleInputError("This bitmask scheduler supports at most 31 cells; the target bound is 9.");
    }
    const ids = new Set();
    return input.cells.map((cell, index) => {
        const id = cell.id === undefined ? `cell-${index}` : String(cell.id);
        if (ids.has(id))
            throw new ScheduleInputError(`Duplicate cell id: ${id}`);
        ids.add(id);
        let regionId = cell.regionId;
        if (typeof cell.region === "string")
            regionId = cell.region;
        if (cell.region && typeof cell.region === "object") {
            regionId = cell.region.id ?? cell.region.regionId ?? regionId ?? `${id}-region`;
        }
        regionId ??= "default";
        if (!regions.has(regionId))
            throw new ScheduleInputError(`Unknown regionId ${regionId} for cell ${id}.`);
        const upgradeMinutes = cell.upgradeMinutes ?? cell.upgradeDurationMinutes ?? cell.upgrade ?? 0;
        const bakeMinutes = cell.bakeMinutes ?? cell.healthObservationMinutes ?? cell.observationMinutes ?? cell.bake ?? 0;
        const duration = cell.durationMinutes ?? cell.occupancyMinutes ?? upgradeMinutes + bakeMinutes;
        if (!Number.isInteger(duration) || duration <= 0) {
            throw new ScheduleInputError(`Cell ${id} occupancy duration must be a positive integer minute count.`);
        }
        if (!Number.isInteger(upgradeMinutes) || upgradeMinutes < 0) {
            throw new ScheduleInputError(`Cell ${id} upgradeMinutes must be a non-negative integer.`);
        }
        if (!Number.isInteger(bakeMinutes) || bakeMinutes < 0) {
            throw new ScheduleInputError(`Cell ${id} bakeMinutes must be a non-negative integer.`);
        }
        const weight = cell.trafficWeight ?? cell.weight ?? cell.traffic ?? 1;
        if (!Number.isFinite(weight) || weight < 0) {
            throw new ScheduleInputError(`Cell ${id} traffic weight must be non-negative.`);
        }
        return { index, id, regionId, weight, upgradeMinutes, bakeMinutes, duration };
    });
}
function resolveEdgeReference(ref, cells) {
    if (typeof ref === "number" && Number.isInteger(ref)) {
        if (ref < 0 || ref >= cells.length)
            throw new ScheduleInputError(`Edge references invalid index ${ref}.`);
        return ref;
    }
    const text = String(ref);
    const byId = cells.findIndex((cell) => cell.id === text);
    if (byId >= 0)
        return byId;
    if (/^\d+$/.test(text)) {
        const index = Number(text);
        if (index < cells.length)
            return index;
    }
    throw new ScheduleInputError(`Edge references unknown cell ${text}.`);
}
function normalizeEdges(input, cells) {
    const rawEdges = input.disasterRecoveryEdges ?? input.drEdges ?? input.mutualBackupEdges ?? input.edges ?? [];
    const masks = cells.map(() => 0);
    const edges = [];
    const seen = new Set();
    for (const raw of rawEdges) {
        let aRef;
        let bRef;
        if (Array.isArray(raw)) {
            [aRef, bRef] = raw;
        }
        else {
            const edge = raw;
            aRef = edge.a ?? edge.from ?? edge.cellA;
            bRef = edge.b ?? edge.to ?? edge.cellB;
        }
        if (aRef === undefined || bRef === undefined) {
            throw new ScheduleInputError("Every DR edge must contain two cell references.");
        }
        const a = resolveEdgeReference(aRef, cells);
        const b = resolveEdgeReference(bRef, cells);
        if (a === b)
            throw new ScheduleInputError(`DR edge ${a}<->${b} must connect distinct cells.`);
        const key = a < b ? `${a}:${b}` : `${b}:${a}`;
        if (seen.has(key))
            continue;
        seen.add(key);
        edges.push([Math.min(a, b), Math.max(a, b)]);
        masks[a] |= 1 << b;
        masks[b] |= 1 << a;
    }
    return { masks, edges };
}
export function createScheduleContext(input) {
    if (!input || typeof input !== "object" || !Array.isArray(input.cells)) {
        throw new ScheduleInputError("scheduleUpgrades requires an object with a cells array.");
    }
    const base = parseInstantToMinute(input.startUtc ?? input.startTime ?? input.globalStart ?? input.start ?? 0);
    const regions = buildRegionMap(input, base);
    const cells = normalizeCells(input, regions);
    const { masks: drMasks, edges: drEdges } = normalizeEdges(input, cells);
    const totalWeight = input.totalTrafficWeight ??
        input.totalWeight ??
        cells.reduce((sum, cell) => sum + cell.weight, 0);
    if (!Number.isFinite(totalWeight) || totalWeight <= 0) {
        throw new ScheduleInputError("totalTrafficWeight must be positive.");
    }
    const absoluteLimit = input.maxUnavailableWeight ?? input.globalTrafficLimit;
    let globalLimit;
    if (typeof absoluteLimit === "number" && Number.isFinite(absoluteLimit)) {
        globalLimit = { mode: "absolute", value: absoluteLimit };
    }
    else {
        globalLimit = normalizeFraction(input.U ?? input.u ?? input.maxUnavailableFraction ?? input.maxUnavailableTrafficFraction ?? input.globalMaxUnavailableFraction);
        if (globalLimit.mode === "fraction")
            globalLimit.totalWeight = totalWeight;
        if (globalLimit.mode === "number")
            globalLimit.totalWeight = totalWeight;
    }
    return { input, base, cells, regions, drMasks, drEdges, globalLimit };
}
function globalBudgetAllows(limit, activeWeight) {
    if (limit.mode === "absolute")
        return activeWeight <= limit.value;
    if (limit.mode === "fraction") {
        return activeWeight * limit.denominator <= limit.numerator * limit.totalWeight;
    }
    return activeWeight <= limit.value * limit.totalWeight;
}
function collectCalendarEvents(region, from, to, events) {
    for (const segment of region.offsets) {
        if (Number.isFinite(segment.start) && segment.start >= from && segment.start <= to) {
            events.add(segment.start);
        }
        if (Number.isFinite(segment.end) && segment.end >= from && segment.end <= to) {
            events.add(segment.end);
        }
        const low = Math.max(from, segment.start);
        const high = Math.min(to, segment.end);
        if (low > high)
            continue;
        for (const window of region.windows) {
            const firstDay = Math.ceil((low + segment.offset - window.endAnchor) / DAY_MINUTES);
            const lastDay = Math.floor((high + segment.offset - window.endAnchor) / DAY_MINUTES);
            for (let day = firstDay; day <= lastDay; day += 1) {
                const instant = day * DAY_MINUTES + window.endAnchor - segment.offset;
                if (instant >= from && instant <= to)
                    events.add(instant);
            }
        }
    }
}
function upperBound(sorted, value) {
    let low = 0;
    let high = sorted.length;
    while (low < high) {
        const middle = Math.floor((low + high) / 2);
        if (sorted[middle] <= value)
            low = middle + 1;
        else
            high = middle;
    }
    return low;
}
function buildHorizon(context) {
    const { base, cells, regions } = context;
    let tailAnchor = base;
    for (const region of regions.values()) {
        const lastSegment = region.offsets[region.offsets.length - 1];
        if (Number.isFinite(lastSegment.start)) {
            tailAnchor = Math.max(tailAnchor, lastSegment.start);
        }
    }
    let tailFittingDuration = 0;
    for (const cell of cells) {
        const region = regions.get(cell.regionId);
        const fitsTail = region.windows.length === 0 || cell.duration <= region.maxGapMinutes;
        if (fitsTail)
            tailFittingDuration += cell.duration;
    }
    return tailAnchor + (cells.length + 2) * DAY_MINUTES + tailFittingDuration;
}
function findSerialHorizon(context, calendars) {
    const { base, cells } = context;
    const runOrder = (order) => {
        let cursor = base;
        for (const index of order) {
            const cell = cells[index];
            const start = calendars.get(cell.regionId).earliestAllowedStart(cursor, cell.duration, cell.id);
            cursor = start + cell.duration;
        }
        return cursor;
    };
    const orders = [
        cells.map((cell) => cell.index),
        cells.map((cell) => cell.index).sort((a, b) => cells[a].duration - cells[b].duration),
        cells.map((cell) => cell.index).sort((a, b) => cells[b].duration - cells[a].duration),
        cells
            .map((cell) => cell.index)
            .sort((a, b) => cells[a].regionId.localeCompare(cells[b].regionId) || cells[a].duration - cells[b].duration),
    ];
    let horizon = null;
    for (const order of orders) {
        try {
            const candidate = runOrder(order);
            horizon = horizon === null ? candidate : Math.min(horizon, candidate);
        }
        catch (error) {
            if (!(error instanceof ScheduleInfeasibleError))
                throw error;
        }
    }
    try {
        const remaining = new Set(cells.map((cell) => cell.index));
        let cursor = base;
        while (remaining.size > 0) {
            let chosen = -1;
            let chosenStart = Number.POSITIVE_INFINITY;
            for (const index of remaining) {
                const cell = cells[index];
                try {
                    const start = calendars
                        .get(cell.regionId)
                        .earliestAllowedStart(cursor, cell.duration, cell.id);
                    if (start < chosenStart) {
                        chosen = index;
                        chosenStart = start;
                    }
                }
                catch (error) {
                    if (!(error instanceof ScheduleInfeasibleError))
                        throw error;
                }
            }
            if (chosen === -1)
                return horizon;
            remaining.delete(chosen);
            cursor = chosenStart + cells[chosen].duration;
        }
        horizon = horizon === null ? cursor : Math.min(horizon, cursor);
    }
    catch (error) {
        if (!(error instanceof ScheduleInfeasibleError))
            throw error;
    }
    return horizon;
}
export function scheduleUpgrades(input) {
    const context = createScheduleContext(input);
    const { base, cells, regions, drMasks, globalLimit } = context;
    const count = cells.length;
    const fullMask = (1 << count) - 1;
    const calendars = new Map();
    for (const region of regions.values()) {
        calendars.set(region.id, new FreezeCalendar(region.windows, region.offsets));
    }
    for (const cell of cells) {
        const region = regions.get(cell.regionId);
        if (region.maxConcurrent < 1) {
            throw new ScheduleInfeasibleError("REGION_CONCURRENCY_ZERO", `区域 ${cell.regionId} 的最大并发数为 ${region.maxConcurrent}，无法启动细胞 ${cell.id}。`, { cellId: cell.id, regionId: cell.regionId });
        }
        if (!globalBudgetAllows(globalLimit, cell.weight)) {
            throw new ScheduleInfeasibleError("CELL_EXCEEDS_GLOBAL_BUDGET", `细胞 ${cell.id} 自身流量权重 ${cell.weight} 已超过全局不可用预算。`, { cellId: cell.id, weight: cell.weight, globalLimit });
        }
        const calendar = calendars.get(region.id);
        calendar.earliestAllowedStart(base, cell.duration, cell.id);
    }
    const serialHorizon = findSerialHorizon(context, calendars);
    const horizon = serialHorizon ?? buildHorizon(context);
    const eventSet = new Set([base, horizon]);
    for (const region of regions.values()) {
        collectCalendarEvents(region, base, horizon, eventSet);
    }
    const calendarEvents = [...eventSet].sort((a, b) => a - b);
    const legalCache = new Map();
    const globalAllow = (activeWeight) => globalBudgetAllows(globalLimit, activeWeight);
    function canStartSubset(time, subsetMask, ends) {
        let activeMask = 0;
        let activeWeight = 0;
        const regionCounts = new Map();
        for (const cell of cells) {
            if (ends[cell.index] !== -1) {
                activeMask |= 1 << cell.index;
                activeWeight += cell.weight;
                regionCounts.set(cell.regionId, (regionCounts.get(cell.regionId) ?? 0) + 1);
            }
        }
        let addedWeight = 0;
        for (const cell of cells) {
            if ((subsetMask & (1 << cell.index)) === 0)
                continue;
            if (drMasks[cell.index] & (subsetMask | activeMask))
                return false;
            if (time + cell.duration > horizon)
                return false;
            const cacheKey = `${cell.index}:${time}`;
            let legal = legalCache.get(cacheKey);
            if (legal === undefined) {
                legal = calendars.get(cell.regionId).isAllowed(time, time + cell.duration);
                legalCache.set(cacheKey, legal);
            }
            if (!legal)
                return false;
            addedWeight += cell.weight;
            regionCounts.set(cell.regionId, (regionCounts.get(cell.regionId) ?? 0) + 1);
        }
        if (!globalAllow(activeWeight + addedWeight))
            return false;
        for (const [regionId, activeCount] of regionCounts) {
            const limit = regions.get(regionId).maxConcurrent;
            if (activeCount > limit)
                return false;
        }
        return true;
    }
    function betterPlan(candidate, current) {
        if (!current)
            return true;
        if (candidate.makespan !== current.makespan)
            return candidate.makespan < current.makespan;
        if (candidate.pendingSum !== current.pendingSum)
            return candidate.pendingSum < current.pendingSum;
        for (let i = 0; i < count; i += 1) {
            if (candidate.starts[i] !== current.starts[i]) {
                return candidate.starts[i] < current.starts[i];
            }
        }
        return false;
    }
    const memo = new Map();
    function dfs(time, pendingMask, ends) {
        const key = `${time}|${pendingMask}|${ends.join(",")}`;
        const cached = memo.get(key);
        if (cached !== undefined)
            return cached;
        let best = null;
        const noActive = ends.every((end) => end === -1);
        if (pendingMask === 0 && noActive) {
            const plan = {
                makespan: time,
                pendingSum: 0,
                starts: new Array(count).fill(-1),
            };
            memo.set(key, plan);
            return plan;
        }
        for (let subset = pendingMask; subset > 0; subset = (subset - 1) & pendingMask) {
            if (!canStartSubset(time, subset, ends))
                continue;
            const nextEnds = ends.slice();
            let nextTime = Number.POSITIVE_INFINITY;
            for (let i = 0; i < count; i += 1) {
                if (ends[i] !== -1)
                    nextTime = Math.min(nextTime, ends[i]);
                if (subset & (1 << i)) {
                    nextEnds[i] = time + cells[i].duration;
                    nextTime = Math.min(nextTime, nextEnds[i]);
                }
            }
            if (!Number.isFinite(nextTime))
                continue;
            for (let i = 0; i < count; i += 1) {
                if (nextEnds[i] === nextTime)
                    nextEnds[i] = -1;
            }
            const suffix = dfs(nextTime, pendingMask & ~subset, nextEnds);
            if (!suffix)
                continue;
            const starts = suffix.starts.slice();
            let startedSum = 0;
            for (let i = 0; i < count; i += 1) {
                if (subset & (1 << i)) {
                    starts[i] = time;
                    startedSum += time;
                }
            }
            const candidate = {
                makespan: suffix.makespan,
                pendingSum: startedSum + suffix.pendingSum,
                starts,
            };
            if (betterPlan(candidate, best))
                best = candidate;
        }
        let waitTime = null;
        let nextEnd = Number.POSITIVE_INFINITY;
        for (const end of ends) {
            if (end !== -1)
                nextEnd = Math.min(nextEnd, end);
        }
        if (Number.isFinite(nextEnd)) {
            waitTime = nextEnd;
        }
        else if (pendingMask !== 0) {
            const eventIndex = upperBound(calendarEvents, time);
            if (eventIndex < calendarEvents.length)
                waitTime = calendarEvents[eventIndex];
        }
        if (waitTime !== null && waitTime > time) {
            const nextEnds = ends.slice();
            if (Number.isFinite(nextEnd)) {
                for (let i = 0; i < count; i += 1) {
                    if (nextEnds[i] === waitTime)
                        nextEnds[i] = -1;
                }
            }
            const suffix = dfs(waitTime, pendingMask, nextEnds);
            if (suffix && betterPlan(suffix, best))
                best = suffix;
        }
        memo.set(key, best);
        return best;
    }
    const emptyEnds = new Array(count).fill(-1);
    let absoluteStarts;
    if (canStartSubset(base, fullMask, emptyEnds)) {
        absoluteStarts = {
            makespan: base + Math.max(...cells.map((cell) => cell.duration)),
            pendingSum: count * base,
            starts: cells.map(() => base),
        };
    }
    else {
        absoluteStarts = dfs(base, fullMask, emptyEnds);
        if (!absoluteStarts) {
            throw new ScheduleInfeasibleError("NO_FEASIBLE_SCHEDULE", "在已证明包含最优解的关键事件边界范围内未找到可行排期。", { horizonMinute: horizon - base });
        }
    }
    const startMinutes = absoluteStarts.starts.map((instant) => instant - base);
    const endMinutes = cells.map((cell, index) => startMinutes[index] + cell.duration);
    const cellsOutput = cells.map((cell, index) => ({
        id: cell.id,
        index,
        regionId: cell.regionId,
        startMinute: startMinutes[index],
        endMinute: endMinutes[index],
        startUtc: new Date(absoluteStarts.starts[index] * 60000).toISOString(),
        endUtc: new Date((absoluteStarts.starts[index] + cell.duration) * 60000).toISOString(),
        upgradeMinutes: cell.upgradeMinutes,
        bakeMinutes: cell.bakeMinutes,
        occupancyMinutes: cell.duration,
        trafficWeight: cell.weight,
    }));
    return {
        feasible: true,
        starts: startMinutes,
        startMinutes,
        startVector: startMinutes,
        ends: endMinutes,
        endMinutes,
        makespanMinutes: Math.max(...endMinutes),
        sumStartMinutes: absoluteStarts.pendingSum - count * base,
        lexKey: startMinutes,
        cells: cellsOutput,
    };
}
export function tryScheduleUpgrades(input) {
    try {
        return { success: true, result: scheduleUpgrades(input) };
    }
    catch (error) {
        if (error instanceof ScheduleInfeasibleError || error instanceof ScheduleInputError) {
            return { success: false, error };
        }
        throw error;
    }
}
function resolveValidationStarts(context, starts, absoluteNumbers) {
    const values = Array.isArray(starts) ? starts : starts.startMinutes;
    if (!Array.isArray(values) || values.length !== context.cells.length) {
        throw new ScheduleInputError("Validation requires one start time per cell.");
    }
    return values.map((value) => {
        if (value instanceof Date || typeof value === "string")
            return parseInstantToMinute(value);
        if (typeof value !== "number" || !Number.isFinite(value)) {
            throw new ScheduleInputError("Invalid validation start time.");
        }
        return absoluteNumbers ? parseInstantToMinute(value) : context.base + value;
    });
}
export function validateSchedule(input, starts, options = {}) {
    const context = createScheduleContext(input);
    const violations = [];
    let absoluteStarts;
    try {
        absoluteStarts = resolveValidationStarts(context, starts, options.absoluteNumbers === true);
    }
    catch (error) {
        if (error instanceof ScheduleInputError) {
            return { valid: false, violations: [{ code: "INVALID_INPUT", message: error.message }] };
        }
        throw error;
    }
    const intervals = context.cells.map((cell, index) => ({
        cell,
        start: absoluteStarts[index],
        end: absoluteStarts[index] + cell.duration,
    }));
    intervals.forEach(({ cell, start }, index) => {
        if (!Number.isInteger(start) || start < context.base) {
            violations.push({
                code: "INVALID_START",
                message: `细胞 ${cell.id} 的启动时刻必须是不早于全局起始时刻的整数分钟。`,
                cellIds: [cell.id],
            });
        }
        const region = context.regions.get(cell.regionId);
        const calendar = new FreezeCalendar(region.windows, region.offsets);
        if (!calendar.isAllowed(start, start + cell.duration)) {
            violations.push({
                code: "FREEZE_WINDOW_OVERLAP",
                message: `细胞 ${cell.id} 的运行区间与区域 ${cell.regionId} 冻结窗有正长度交集。`,
                cellIds: [cell.id],
            });
        }
        if (!globalBudgetAllows(context.globalLimit, cell.weight)) {
            violations.push({
                code: "GLOBAL_BUDGET_EXCEEDED",
                message: `细胞 ${cell.id} 自身超过全局流量预算。`,
                cellIds: [cell.id],
            });
        }
        if (region.maxConcurrent < 1) {
            violations.push({
                code: "REGION_CONCURRENCY_EXCEEDED",
                message: `区域 ${region.id} 并发上限为 ${region.maxConcurrent}。`,
                cellIds: [cell.id],
            });
        }
        if (index === 0)
            return;
    });
    for (const [a, b] of context.drEdges) {
        const left = intervals[a];
        const right = intervals[b];
        if (left.start < right.end && right.start < left.end) {
            violations.push({
                code: "DR_OVERLAP",
                message: `灾备配对细胞 ${left.cell.id} 与 ${right.cell.id} 的运行区间重叠。`,
                cellIds: [left.cell.id, right.cell.id],
            });
        }
    }
    const eventTimes = [...new Set(intervals.flatMap((interval) => [interval.start, interval.end]))].sort((a, b) => a - b);
    for (const time of eventTimes) {
        const active = intervals.filter((interval) => interval.start <= time && time < interval.end);
        const activeWeight = active.reduce((sum, interval) => sum + interval.cell.weight, 0);
        if (!globalBudgetAllows(context.globalLimit, activeWeight)) {
            violations.push({
                code: "GLOBAL_BUDGET_EXCEEDED",
                message: `时刻 ${new Date(time * 60000).toISOString()} 全网不可用流量权重 ${activeWeight} 超过上限。`,
                minute: time - context.base,
                cellIds: active.map((interval) => interval.cell.id),
            });
        }
        const byRegion = new Map();
        for (const interval of active) {
            const list = byRegion.get(interval.cell.regionId) ?? [];
            list.push(interval);
            byRegion.set(interval.cell.regionId, list);
        }
        for (const [regionId, list] of byRegion) {
            const limit = context.regions.get(regionId).maxConcurrent;
            if (list.length > limit) {
                violations.push({
                    code: "REGION_CONCURRENCY_EXCEEDED",
                    message: `区域 ${regionId} 在时刻 ${new Date(time * 60000).toISOString()} 有 ${list.length} 个细胞并发，上限 ${limit}。`,
                    minute: time - context.base,
                    cellIds: list.map((interval) => interval.cell.id),
                });
            }
        }
    }
    return { valid: violations.length === 0, violations };
}
export function assertValidSchedule(input, result) {
    const validation = validateSchedule(input, result.startMinutes);
    if (!validation.valid) {
        throw new ScheduleInfeasibleError("INVALID_RESULT_SCHEDULE", validation.violations.map((violation) => violation.message).join("\n"), validation.violations);
    }
}
export const schedule = scheduleUpgrades;
export const planUpgrades = scheduleUpgrades;
export const solveSchedule = scheduleUpgrades;
export const optimizeSchedule = scheduleUpgrades;
export const verifySchedule = validateSchedule;
export default scheduleUpgrades;
//# sourceMappingURL=scheduler.js.map