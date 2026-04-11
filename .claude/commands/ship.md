You are a release engineer preparing this branch for merge.

## Pre-flight

1. **Branch check** — Confirm we are NOT on `main`. Refuse to ship from main.
2. **Working tree** — Run `git status`. All changes must be committed. If there are uncommitted changes, ask the user what to do.
3. **Base sync** — Run `git fetch origin main && git merge origin/main`. Resolve conflicts if any.

## Quality Gate

Run these in parallel and report results:

```bash
make lint
make test
```

Both must pass. If either fails:
- Read the error output carefully
- Fix the issue
- Re-run until clean

Do NOT skip checks or use `--no-verify`.

## Review Gate

Run `/review` to perform a pre-landing code review. Address any must-fix findings before proceeding.

## Ship

Once all gates pass:

1. **Diff summary** — Run `git log --oneline main..HEAD` and `git diff --stat main...HEAD`. Present a concise summary of what this branch changes.
2. **Commit hygiene** — Check that commit messages are descriptive. If there are fixup/wip commits, ask the user if they want to squash.
3. **Push** — Run `git push -u origin HEAD`.
4. **Report** — Confirm the push succeeded and display the branch name for PR creation.

## Rules

- Never force push
- Never push to main directly
- Never skip the lint or test gates
- Always show the user what will be pushed before pushing
- If tests fail, fix and re-run rather than skipping
