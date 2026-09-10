"""Enforce both Go statement and executable-block line coverage, including cmd."""
from pathlib import Path
import re
import sys

blocks = {}
for line in Path(sys.argv[1] if len(sys.argv) > 1 else "coverage.out").read_text().splitlines()[1:]:
    location, statements, count = line.split()
    old = blocks.get(location, (int(statements), 0))
    blocks[location] = (int(statements), max(old[1], int(count)))
covered_lines, all_lines = set(), set()
covered_statements = total_statements = 0
for location, (statements, count) in blocks.items():
    match = re.fullmatch(r"(.+):(\d+)\.\d+,(\d+)\.\d+", location)
    if not match:
        raise SystemExit(f"Invalid coverage location: {location}")
    total_statements += statements
    if count:
        covered_statements += statements
    if statements:
        lines = {(match[1], number) for number in range(int(match[2]), int(match[3]) + 1)}
        all_lines.update(lines)
        if count:
            covered_lines.update(lines)
if not total_statements or not all_lines:
    raise SystemExit("Coverage report is empty")
statement_rate = 100 * covered_statements / total_statements
line_rate = 100 * len(covered_lines) / len(all_lines)
print(f"Go statements: {statement_rate:.2f}%; executable block lines: {line_rate:.2f}% ({len(covered_lines)}/{len(all_lines)})")
if min(statement_rate, line_rate) < 80:
    raise SystemExit("Required coverage is at least 80%")
