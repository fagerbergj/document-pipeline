<!-- Conventional-commit title: feat(scope): … / fix(scope): … / docs(scope): … -->

## What & why

<!-- One or two lines: what changed and the motivation. The story goes here, not in the source. -->

Closes #

## Spec (non-trivial changes)

<!-- Skip for a trivial fix. Otherwise summarize, or link the issue's spec:
- **Scope / out of scope** -
- **Forbidden actions** -
- **Output contract** -
- **Test cases** - the 2–3 concrete cases now covered by tests -->

## Checklist

- [ ] `go test ./...` passes (`-race` for concurrency changes)
- [ ] `make vet && make fmt` clean
- [ ] Changed `openapi.yaml`? Regenerated and committed the generated files in this PR
- [ ] No hand-edits to generated files
- [ ] Comments say what the code *can't* - no narration or change history
