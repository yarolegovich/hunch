# 04 · Pre-commit guard

Stop API keys, passwords and leftover debug code before they're committed.
Pattern scanners catch `AKIA...`; hunch also catches the hand-rolled token in
a test fixture and the `// TODO remove` that disables a check.

You'll use `ask` on the staged diff as one record, and hunch's exit codes to
block the commit.

## 1. Try it on what you've staged

```sh
git diff --cached | hunch --in whole "Does this diff add a secret, such as an API key, password or token?"
```

```text
yes (0.97)  diff --git a/billing/client.go b/billing/client.go ...
```

## 2. Ask everything at once

Each question goes into the same call, so checking for three things costs
about the same as one. Commit this file to the repo:

```yaml
# .githooks/guard.yaml
context: A staged git diff about to be committed. Lines starting with + are added.
questions:
  secret:
    type: noul
    q: Does this diff add a secret, such as an API key, password, token or private key?
    no_when: Placeholders like "changeme", values read from the environment, or test keys clearly marked as fake
  debug:
    type: noul
    q: Does this diff add leftover debugging code, such as print statements, disabled checks or hard-coded test values?
  personal:
    type: noul
    q: Does this diff add personal data about real people, such as emails, phone numbers or addresses?
keep: secret.p > 0.5 || debug.p > 0.8 || personal.p > 0.7
```

`keep` decides when hunch prints the record, and so its exit code: **0** if
something was found, **1** if the diff is clean.

```sh
git diff --cached | hunch ask -f .githooks/guard.yaml --in whole --json | jq .hunch
```

```json
{"secret": {"p": 0.97}, "debug": {"p": 0.96}, "personal": {"p": 0.02}}
```

## 3. The hook

```sh
#!/usr/bin/env bash
# .githooks/pre-commit
git diff --cached --quiet && exit 0   # nothing staged

verdict=$(git diff --cached | hunch ask -f .githooks/guard.yaml --in whole --json)
case $? in
  0)
    echo "hunch: this commit looks like it adds something it shouldn't:" >&2
    jq -r '.hunch | to_entries[] | "  \(.key): \(.value.p)"' <<<"$verdict" >&2
    echo "Commit anyway with: git commit --no-verify" >&2
    exit 1 ;;
  1)
    exit 0 ;;
  *)
    echo "hunch: check skipped (offline or no API key)" >&2
    exit 0 ;;
esac
```

Turn it on for the repo:

```sh
chmod +x .githooks/pre-commit
git config core.hooksPath .githooks
```

Any other exit code (no key, no network, API down) lets the commit through
with a warning, so the hook never stands between you and your work.

## 4. Also in CI

Hooks are opt-in per clone. Run the same check on every pull request, so it
holds for everyone:

```yaml
- name: Guard
  env:
    TYPESAFE_API_KEY: ${{ secrets.TYPESAFE_API_KEY }}
  run: |
    git fetch origin "$GITHUB_BASE_REF"
    if git diff "origin/$GITHUB_BASE_REF...HEAD" \
        | hunch ask -f .githooks/guard.yaml --in whole --json > verdict.json; then
      jq . verdict.json
      exit 1
    fi
```

The checkout step needs `fetch-depth: 0` for the base branch diff to work.

## Notes

- **Speed.** A typical staged diff is judged in well under a second.
- **Large diffs.** Big generated changes can go past the default context
  limit; add `--long-context`, or leave out lock files with
  `git diff --cached -- . ':!*.lock'`.
- **Not a replacement.** Keep a pattern-based scanner like gitleaks too;
  hunch is the second pair of eyes for what patterns miss.
