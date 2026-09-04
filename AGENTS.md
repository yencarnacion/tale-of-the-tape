# Workspace data-safety rules

## Tags and journal data

- Before running or implementing any operation that could delete tag
  definitions, trade-to-tag assignments, trade notes, or day notes, inspect
  and report the exact records at risk to the user.
- Warn the user explicitly that the proposed action can delete those records
  and wait for clear confirmation before making the change.
- After confirmation and before the destructive operation, create and verify a
  SQLite backup unless the user explicitly declines one.
- Do not treat a general request to import, rebuild, reconcile, or recalculate
  data as permission to delete tags or journal data.
- Prefer identity-preserving updates. Invalidate or replace only records whose
  underlying execution membership actually changed.
