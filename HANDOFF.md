# Ohara — Agent Handoff Protocol

When an agent pauses, completes, or escalates work, write a handoff so the next agent (or human) can continue without re-discovery.

## Handoff Format

```text
Status: done|blocked|needs-input
Findings: <max 5 bullets describing what was learned>
Changed: <files modified or "none">
Verify: <checks run or needed>
Next: <single next action for the next agent>
```

## When to Write

- End of an autonomous workflow session
- Escalating to another agent (e.g., `fast` → `deep`)
- Pausing work mid-stream
- After resolving a bug or completing a feature

## Where to Write

- For long-running work: `.opencode/handoff.md`
- For session-end summaries: use `mem_session_summary` and `mem_save` for persistent memory

## Key Rules

- One next action. If multiple paths exist, pick the most critical.
- Findings are facts, not speculation. If uncertain, say so.
- Changed files should be specific paths, not globs.
- If the handoff is to a human, add context in plain prose after the compact block.
