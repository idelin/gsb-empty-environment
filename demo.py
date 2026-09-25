"""运行样例:跨偏移转换的有效时长正算与反求。

场景:合成偏移表(基准 +08:00)
- 2024-03-10 02:00 前拨一小时(+8 -> +9),墙钟 02:00 直接跳到 03:00
- 2024-11-03 02:00 回拨一小时(+9 -> +8),墙钟 01:00-02:00 走两遍
日历:每日 09:00-12:00、13:00-17:00 开放;2024-03-11 14:00-15:00 例外关闭。
"""

from datetime import datetime, time, timedelta

from effective_time import (
    ClosedInterval,
    DailyWindow,
    Duration,
    EffectiveCalendar,
    OffsetTable,
    OffsetTransition,
    WallTime,
)

H8, H9 = timedelta(hours=8), timedelta(hours=9)

table = OffsetTable(
    H8,
    [
        OffsetTransition(WallTime(datetime(2024, 3, 10, 2, 0)), H8, H9),   # 前拨
        OffsetTransition(WallTime(datetime(2024, 11, 3, 2, 0)), H9, H8),   # 回拨
    ],
)

calendar = EffectiveCalendar(
    table,
    [DailyWindow(time(9, 0), time(12, 0)), DailyWindow(time(13, 0), time(17, 0))],
    [ClosedInterval(WallTime(datetime(2024, 3, 11, 14, 0)), WallTime(datetime(2024, 3, 11, 15, 0)))],
)


def show(label, instant):
    print(f"  {label}: 墙钟 {table.instant_to_wall(instant).value}  (Instant.micros={instant.micros})")


def main():
    print("== 场景 1:前拨日开放 01:00-04:00,实际只有 2 小时 ==")
    spring = EffectiveCalendar(table, [DailyWindow(time(1, 0), time(4, 0))])
    s1 = table.wall_to_instant(WallTime(datetime(2024, 3, 10, 1, 0)))
    e1 = table.wall_to_instant(WallTime(datetime(2024, 3, 10, 4, 0)))
    print(f"  墙钟跨度 3 小时,有效时长 = {spring.effective_duration(s1, e1).total_seconds() / 3600} 小时")

    print("== 场景 2:正算,2024-03-08 10:00 -> 2024-03-12 16:00 ==")
    start = table.wall_to_instant(WallTime(datetime(2024, 3, 8, 10, 0)))
    end = table.wall_to_instant(WallTime(datetime(2024, 3, 12, 16, 0)))
    show("起点", start)
    show("终点", end)
    duration = calendar.effective_duration(start, end)
    print(f"  有效时长 = {duration}  ({duration.total_seconds() / 3600} 小时)")
    for lo, hi in calendar.open_intervals_between(start, end):
        print(f"    开放段 {table.instant_to_wall(lo).value} ~ {table.instant_to_wall(hi).value}"
              f"  = {(hi - lo).total_seconds() / 3600} 小时")

    print("== 场景 3:反求,从起点加同样的有效时长 ==")
    back = calendar.add(start, duration)
    show("反求终点", back)
    print(f"  与正算终点一致: {back == end}")

    print("== 场景 4:反求落在关闭段,跳到下一开放段 ==")
    morning = table.wall_to_instant(WallTime(datetime(2024, 3, 8, 9, 0)))
    landed = calendar.add(morning, Duration.of(hours=3))  # 恰好填满 09:00-12:00
    show("09:00 + 3h", landed)
    print("  (12:00-13:00 为午休间隙,边界落点跳到 13:00)")


if __name__ == "__main__":
    main()
