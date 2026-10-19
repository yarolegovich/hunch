# 03 · Issue triage bot

Every new GitHub issue gets labeled, bug reports with no way to reproduce
them get a polite request for details, and spam is closed before anyone sees
it. It's a few lines of shell in a GitHub Actions workflow.

You'll use `hunch ask` on the issue as one record, and act on the answers
with `gh`.

## 1. The questions

```yaml
# .github/hunch/triage.yaml
context: A new GitHub issue for a command-line tool. Title first, then the body.
questions:
  kind:
    type: choice
    q: What kind of issue is this?
    options:
      - bug=Something that should work is broken
      - feature=A request for new behavior
      - question=Asking how to do something
  complete:
    type: noul
    q: Does the issue give enough to reproduce a bug, such as the command run, the version and the output?
  spam:
    type: noul
    q: Is this spam, an ad or unrelated to the project?
```

Try it on an issue that already exists:

```sh
gh issue view 17 --json title,body -q '.title + "\n\n" + .body' \
  | hunch ask -f .github/hunch/triage.yaml --in whole --json | jq -c .hunch
```

What it says about three typical issues:

| Issue | kind | complete | spam |
| - | - | - | - |
| "hunch crashes. it doesnt work anymore pls fix" | bug (1.00) | 0.04 | 0.07 |
| "pick: --conf ignored with -o", with version, command and output | bug (1.00) | 0.87 | 0.03 |
| "Best crypto signals 2026!!! Join our telegram" | feature (0.72) | 0.02 | 0.98 |

## 2. The workflow

Store your key as the `TYPESAFE_API_KEY` repository secret, create the labels
once (`gh label create needs-info`, ...), and add:

```yaml
# .github/workflows/triage.yml
name: Triage issues
on:
  issues:
    types: [opened]

permissions:
  contents: read
  issues: write

jobs:
  triage:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version: stable
      - run: go install github.com/yarolegovich/hunch@latest

      - name: Triage
        env:
          GH_TOKEN: ${{ github.token }}
          ISSUE: ${{ github.event.issue.number }}
          TYPESAFE_API_KEY: ${{ secrets.TYPESAFE_API_KEY }}
        run: |
          jq -r '.issue.title + "\n\n" + (.issue.body // "")' "$GITHUB_EVENT_PATH" \
            | hunch ask -f .github/hunch/triage.yaml --in whole --json > verdict.json
          read -r kind conf complete spam < <(jq -r '.hunch
            | "\(.kind.choice) \(.kind.confidence) \(.complete.p) \(.spam.p)"' verdict.json)

          if (( $(echo "$spam > 0.95" | bc) )); then
            gh issue close "$ISSUE" --reason "not planned" --comment "Closed automatically as spam."
            gh issue edit "$ISSUE" --add-label spam
            exit 0
          fi

          if (( $(echo "$conf >= 0.6" | bc) )); then
            gh issue edit "$ISSUE" --add-label "$kind"
          fi

          if [ "$kind" = bug ] && (( $(echo "$complete < 0.3" | bc) )); then
            gh issue edit "$ISSUE" --add-label needs-info
            gh issue comment "$ISSUE" --body-file .github/hunch/needs-info.md
          fi
```

with a friendly reply next to the questions:

```markdown
<!-- .github/hunch/needs-info.md -->
Thanks for the report! To look into it, we need a bit more:

- the command you ran
- the output you got, and what you expected
- `hunch version`
```

## 3. Route to people

Add an `area` question and assign whoever owns it:

```yaml
  area:
    type: choice
    q: Which part of the project is this about?
    options:
      - providers=TypeSafe, OpenRouter, OpenCode Zen, API keys
      - output=JSONL, TSV, --raw, file routing
      - install=Homebrew, go install, release binaries
      - other
```

```sh
case $(jq -r .hunch.area.choice verdict.json) in
  providers) gh issue edit "$ISSUE" --add-assignee alice ;;
  install)   gh issue edit "$ISSUE" --add-assignee bob ;;
esac
```

## Notes

- **Thresholds are the policy.** Closing is the one irreversible step, so it
  needs `spam > 0.95`; a label is cheap to fix, so `0.6` is enough.
- **Edited issues.** Add `edited` to `types` to re-check an issue after the
  reporter fills in the details, and remove `needs-info` when `complete` is
  high.
- **Same idea, other inboxes.** Anything that delivers a webhook or runs a
  script per message (a helpdesk, a mail filter, a Slack bot) can pipe the
  text through the same questions file.
