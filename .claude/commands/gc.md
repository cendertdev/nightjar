# Git Commit & Push Command

Create git commits with Conventional Commits messages, automatic change analysis, and push to origin.

## Command Usage

- `/gc` - Commit and push without testing
- `/gc --test` - Commit and push with pre-push testing

## Workflow Instructions

When this command is invoked, execute the following workflow:

### 1. Analyze Repository State

Run these commands in parallel to understand current state:
- `git status` (never use -uall flag)
- `git diff` to see staged changes
- `git diff HEAD` to see all changes including unstaged
- `git log --oneline -5` to see recent commit style

If the content in the current working tree is a large number of files, group them by content, and plan to make multiple commits sequentially to preserve logical order of large edits.

### 2. Determine Commit Type

Analyze the changes and classify as one of:
- `feat:` - New feature or capability
- `fix:` - Bug fix
- `refactor:` - Code restructuring (no behavior change)
- `docs:` - Documentation only changes
- `test:` - Test additions or modifications
- `chore:` - Build, tools, or config changes
- `perf:` - Performance improvements
- `style:` - Code formatting (no logic change)

### 3. Generate Commit Message

Create a Conventional Commits message:

**Format:**
```
<type>: <short description>

<optional detailed body>
```

**Guidelines:**
- Subject: Clear, concise (max 50 chars)
- Body: Optional, explain "why" not "what"
- Use imperative mood ("add" not "added")
- Do NOT add any lines denoting coauthorship

**Examples:**
```
feat: add distributed rate limiting with global semaphore

fix: resolve queue item locking race condition

docs: update Kubernetes deployment troubleshooting

refactor: extract trial validation to service layer

perf: optimize trial metrics queries with eager loading
```

### 4. Stage Relevant Files

Run `git add` for modified files, excluding:
- Secret files (*.env, credentials.json, *.pem, *.key)
- Large binary files (unless intentional)
- Temporary files

### 5. Optional Testing (if --test flag provided)

If command includes `--test` or `-t`:
- Run `make test`
- If tests fail: STOP and report failures
- If tests pass: Continue to commit

### 6. Create Commit

Run commit using HEREDOC format for proper multiline:
```bash
git commit -m "$(cat <<'EOF'
<commit message here>
EOF
)"
```

### 7. Verify Commit Success

Run `git status` and `git log -1` to confirm commit was created.

### 8. Push to Origin

Execute push workflow:

**Scenario 1: Clean push**
```bash
git push origin <branch>
```
If successful, report completion.

**Scenario 2: Push rejected (behind remote)**
```bash
git push origin <branch>
# If rejected (fetch first), auto-rebase:

git pull --rebase origin <branch>
# Wait for rebase to complete

git push origin <branch>
```

**Scenario 3: Rebase conflicts**
If `git pull --rebase` reports conflicts:
- STOP immediately
- Report conflicted files
- Provide resolution instructions:
  ```
  ⚠️ Rebase conflict detected in <file>

  To resolve:
  1. Fix conflicts in <file>
  2. git add <file>
  3. git rebase --continue
  4. git push origin <branch>
  ```

## Safety Protocols

**ALWAYS:**
- ✅ Use standard git commands (status, diff, log, add, commit, push)
- ✅ Use `git pull --rebase` for clean history
- ✅ Respect pre-commit hooks (automatic `make lint`)
- ✅ Verify commit created before pushing
- ✅ Report clear error messages

**NEVER:**
- ❌ Use `--force` push (unless explicitly requested by user)
- ❌ Use `--no-verify` to skip hooks
- ❌ Use `git reset --hard` (destructive)
- ❌ Commit files with secrets (.env, credentials.json)
- ❌ Push to main/master without warning if using --force

## Error Handling

**Pre-commit hook failed:**
- Report: "Pre-commit hook failed (make lint)"
- Suggest: "Run 'make format' to auto-fix, then try again"

**Tests failed (with --test):**
- Report: "Tests failed, commit not created"
- Show: Failed test output
- Suggest: "Fix failing tests and try again"

**Push rejected (behind remote):**
- Auto-handle with `git pull --rebase`
- Report: "Auto-rebasing on remote changes..."

**Rebase conflicts:**
- Stop and provide clear resolution steps
- Don't attempt automatic resolution

## Expected Output

After successful execution, report:
```
✓ Analyzed changes in <N> files
✓ Generated commit: <type>: <subject>
✓ Staged <N> files
[✓ Tests passed (if --test used)]
✓ Commit created: <commit-hash>
✓ Pushed to origin/<branch>
```

## Time Savings

This command reduces commit workflow from ~2-3 minutes to ~15-20 seconds (80-85% faster).