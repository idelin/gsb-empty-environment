import argparse

from .planner import QueryPlanner
from .sample_data import sample_database


def main():
    parser = argparse.ArgumentParser(description="Run SQL against the local sample relational database.")
    parser.add_argument("sql_file", help="file containing one supported SELECT statement")
    parser.add_argument("--explain", action="store_true", help="print the rewritten logical plan")
    args = parser.parse_args()

    with open(args.sql_file, "r", encoding="utf-8") as source:
        sql = source.read()
    planner = QueryPlanner(sample_database())
    plan = planner.plan(sql)
    if args.explain:
        print(plan.explain())
    print(" | ".join(plan.schema))
    for row in plan.execute(planner.database):
        print(" | ".join("NULL" if value is None else str(value) for value in row))


if __name__ == "__main__":
    main()
