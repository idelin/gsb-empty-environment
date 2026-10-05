import { describe, expect, test } from "bun:test";
import {
  DuplicateEventError,
  GroupedAggregator,
  UnknownEventError,
} from "../src/grouped-aggregator";

describe("basic aggregates", () => {
  test("count, sum, min and top2 are maintained on insert", () => {
    const agg = new GroupedAggregator();
    agg.insert("e1", "a", 5);
    agg.insert("e2", "a", 3);
    agg.insert("e3", "a", 9);
    agg.insert("e4", "b", 1);

    expect(agg.stats("a")).toEqual({
      group: "a",
      count: 3,
      sum: 17,
      min: 3,
      top2: [
        { eventId: "e2", value: 3 },
        { eventId: "e1", value: 5 },
      ],
    });
    expect(agg.stats("b")).toEqual({
      group: "b",
      count: 1,
      sum: 1,
      min: 1,
      top2: [{ eventId: "e4", value: 1 }],
    });
  });

  test("top2 holds a single entry when the group has one event", () => {
    const agg = new GroupedAggregator();
    agg.insert("only", "g", 42);
    expect(agg.stats("g")!.top2).toEqual([{ eventId: "only", value: 42 }]);
  });

  test("stats is undefined for a group that never existed", () => {
    const agg = new GroupedAggregator();
    expect(agg.stats("nope")).toBeUndefined();
  });
});

describe("retraction", () => {
  test("removing the current min reveals the next smallest value", () => {
    const agg = new GroupedAggregator();
    agg.insert("e1", "g", 1);
    agg.insert("e2", "g", 2);
    agg.insert("e3", "g", 3);
    expect(agg.stats("g")!.min).toBe(1);

    agg.retract("e1");
    const stats = agg.stats("g")!;
    expect(stats.min).toBe(2);
    expect(stats.count).toBe(2);
    expect(stats.sum).toBe(5);
    expect(stats.top2).toEqual([
      { eventId: "e2", value: 2 },
      { eventId: "e3", value: 3 },
    ]);

    agg.retract("e2");
    expect(agg.stats("g")!.min).toBe(3);
  });

  test("retracting an unknown id throws UnknownEventError", () => {
    const agg = new GroupedAggregator();
    expect(() => agg.retract("ghost")).toThrow(UnknownEventError);
  });

  test("duplicate retraction is rejected and does not corrupt aggregates", () => {
    const agg = new GroupedAggregator();
    agg.insert("e1", "g", 7);
    agg.insert("e2", "g", 8);
    agg.retract("e1");

    expect(() => agg.retract("e1")).toThrow(UnknownEventError);
    expect(agg.stats("g")).toEqual({
      group: "g",
      count: 1,
      sum: 8,
      min: 8,
      top2: [{ eventId: "e2", value: 8 }],
    });
  });

  test("duplicate insert of a live id throws DuplicateEventError", () => {
    const agg = new GroupedAggregator();
    agg.insert("e1", "g", 1);
    expect(() => agg.insert("e1", "g", 2)).toThrow(DuplicateEventError);
    expect(agg.stats("g")!.count).toBe(1);
  });

  test("an id can be reused after retraction", () => {
    const agg = new GroupedAggregator();
    agg.insert("e1", "g", 1);
    agg.retract("e1");
    agg.insert("e1", "g", 2);
    expect(agg.stats("g")!.sum).toBe(2);
  });
});

describe("empty group deletion and bounded index", () => {
  test("a group is deleted once its last event is retracted", () => {
    const agg = new GroupedAggregator();
    agg.insert("e1", "a", 1);
    agg.insert("e2", "b", 2);
    expect(agg.groupCount).toBe(2);

    agg.retract("e1");
    expect(agg.groupCount).toBe(1);
    expect(agg.stats("a")).toBeUndefined();
  });

  test("index size is bounded by live events and shrinks on retraction", () => {
    const agg = new GroupedAggregator();
    for (let i = 0; i < 100; i++) agg.insert(`e${i}`, `g${i % 3}`, i);
    expect(agg.indexSize()).toBe(100);
    expect(agg.size).toBe(100);

    for (let i = 0; i < 100; i += 2) agg.retract(`e${i}`);
    expect(agg.indexSize()).toBe(50);
    expect(agg.indexSize()).toBe(agg.size);

    for (let i = 1; i < 100; i += 2) agg.retract(`e${i}`);
    expect(agg.indexSize()).toBe(0);
    expect(agg.groupCount).toBe(0);
  });
});

describe("atomic group change", () => {
  test("update moves an event between groups as one modification", () => {
    const agg = new GroupedAggregator();
    agg.insert("e1", "a", 10);
    agg.insert("e2", "a", 20);
    agg.insert("e3", "b", 30);

    agg.update("e1", "b", 5);

    expect(agg.stats("a")).toEqual({
      group: "a",
      count: 1,
      sum: 20,
      min: 20,
      top2: [{ eventId: "e2", value: 20 }],
    });
    expect(agg.stats("b")).toEqual({
      group: "b",
      count: 2,
      sum: 35,
      min: 5,
      top2: [
        { eventId: "e1", value: 5 },
        { eventId: "e3", value: 30 },
      ],
    });
    expect(agg.size).toBe(3);
  });

  test("moving the last event out deletes the source group", () => {
    const agg = new GroupedAggregator();
    agg.insert("e1", "a", 1);
    agg.moveToGroup("e1", "b");

    expect(agg.stats("a")).toBeUndefined();
    expect(agg.groupCount).toBe(1);
    expect(agg.stats("b")!.count).toBe(1);
  });

  test("moveToGroup keeps the value", () => {
    const agg = new GroupedAggregator();
    agg.insert("e1", "a", 7);
    agg.moveToGroup("e1", "b");
    expect(agg.stats("b")!.sum).toBe(7);
  });

  test("a failed update leaves all state untouched", () => {
    const agg = new GroupedAggregator();
    agg.insert("e1", "a", 1);
    const before = agg.stats("a");

    expect(() => agg.update("ghost", "b", 2)).toThrow(UnknownEventError);
    expect(() => agg.update("e1", "b", Number.NaN)).toThrow(RangeError);

    expect(agg.stats("a")).toEqual(before);
    expect(agg.stats("b")).toBeUndefined();
    expect(agg.size).toBe(1);
    expect(agg.indexSize()).toBe(1);
  });

  test("updating to identical group and value is a no-op", () => {
    const agg = new GroupedAggregator();
    agg.insert("e1", "a", 1);
    agg.update("e1", "a", 1);
    expect(agg.stats("a")!.count).toBe(1);
    expect(agg.indexSize()).toBe(1);
  });
});

describe("tie ranking", () => {
  test("equal values are ordered deterministically by eventId", () => {
    const agg = new GroupedAggregator();
    agg.insert("e-c", "g", 5);
    agg.insert("e-a", "g", 5);
    agg.insert("e-b", "g", 5);

    expect(agg.stats("g")!.top2).toEqual([
      { eventId: "e-a", value: 5 },
      { eventId: "e-b", value: 5 },
    ]);
    expect(agg.stats("g")!.min).toBe(5);
  });

  test("retracting one of the tied leaders promotes the next tied event", () => {
    const agg = new GroupedAggregator();
    agg.insert("e-a", "g", 5);
    agg.insert("e-b", "g", 5);
    agg.insert("e-c", "g", 5);

    agg.retract("e-a");
    expect(agg.stats("g")!.top2).toEqual([
      { eventId: "e-b", value: 5 },
      { eventId: "e-c", value: 5 },
    ]);
  });

  test("ties do not disturb value ordering", () => {
    const agg = new GroupedAggregator();
    agg.insert("z", "g", 1);
    agg.insert("a", "g", 2);
    agg.insert("m", "g", 1);

    expect(agg.stats("g")!.top2).toEqual([
      { eventId: "m", value: 1 },
      { eventId: "z", value: 1 },
    ]);
  });
});

describe("input validation", () => {
  test("non-finite values are rejected on insert and update", () => {
    const agg = new GroupedAggregator();
    expect(() => agg.insert("e1", "g", Number.POSITIVE_INFINITY)).toThrow(RangeError);
    agg.insert("e2", "g", 1);
    expect(() => agg.update("e2", "g", Number.NaN)).toThrow(RangeError);
    expect(agg.stats("g")!.sum).toBe(1);
  });
});
