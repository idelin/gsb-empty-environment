import os
import tempfile
from merge_join import merge_join


def print_small_left_outer():
    employees = [
        (None, "unknown department"),
        (1, "Alice"),
        (1, "Bob"),
        (2, "Cara"),
        (4, "Dana"),
    ]
    departments = [
        (None, "ignored null department"),
        (1, "Platform"),
        (1, "Data"),
        (1, "Tools"),
        (3, "Sales"),
    ]

    print("small left outer join:")
    for employee, department in merge_join(employees, departments, 0, how="left"):
        print(f"  {employee} <- {department}")


def print_spillable_large_group():
    large_left = [(7, f"E{index:03d}") for index in range(150)]
    large_right = [(7, f"D{index}") for index in range(3)]
    stats = []
    spill_file_seen = False

    def observe(info):
        nonlocal spill_file_seen
        stats.append(info)
        spill_file_seen = info["left_spill_path"] is not None and os.path.exists(
            info["left_spill_path"]
        )

    with tempfile.TemporaryDirectory(prefix="merge_join_demo_") as spill_dir:
        rows = list(
            merge_join(
                large_left,
                large_right,
                0,
                max_in_memory=50,
                spill_directory=spill_dir,
                group_callback=observe,
            )
        )

    info = stats[0]
    print("large bounded group:")
    print(f"  left rows: {info['left_count']}")
    print(f"  right rows: {info['right_count']}")
    print(f"  output rows: {len(rows)}")
    print(f"  memory bound per group: 50 rows")
    print(f"  left spilled: {info['left_spilled']}")
    print(f"  spill file existed while processing group: {spill_file_seen}")


if __name__ == "__main__":
    print_small_left_outer()
    print()
    print_spillable_large_group()
