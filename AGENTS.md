# AGENTS.md

## Style
- Receiver name is always `me`.

## Commits
- Single subject: `<type>: <one-line message>`.
- Multiple changes: `<type>: <summary of most significant change>`, blank line, then one `<type>: <message>` line per change.
- Never mention `docs:` (or `doc:`) in a commit that did anything else; use it only when documenting is the one and only thing the commit did.

Example:

```
test: cover Error formatting, matching and handler branches

- test: cover Error formatting, matching and default handler branches
- test: cover Error default handler fallback
```
