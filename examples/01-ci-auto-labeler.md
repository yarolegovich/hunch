# 01 · CI auto-labeler

Label every pull request by what kind of change it is, flag risky ones, and
do it in a GitHub Actions job that costs a fraction of a cent per PR.

You'll use `hunch pick` on a whole input (the PR), then `hunch ask` to answer
several questions about it in one call.

## 1. Try it locally

Feed a PR's title, description and diff to hunch as one record with
`--in whole`:

```sh
pr() { gh pr view "$1" --json title,body -q '.title + "\n\n" + .body'; gh pr diff "$1"; }

pr 42 | hunch pick --in whole "What kind of change is this pull request?" \
  -c feature,fix,refactor,docs,chore
```

```text
fix 0.94  Retry webhook delivery on 5xx ...
```

Choices get sharper with a short description each. A value with `=` isn't
split on commas, so descriptions can contain them:

```sh
pr 42 | hunch pick --in whole "What kind of change is this pull request?" \
  -c 'feature=Adds new user-facing behavior' \
  -c 'fix=Corrects broken behavior, without adding features' \
  -c 'refactor=Restructures code, behavior stays the same' \
  -c 'docs=Only documentation, comments or examples' \
  -c 'chore=Dependencies, CI, build or tooling'
```

## 2. Check it against history

Before trusting it in CI, run it over recently merged PRs and compare with
the labels people gave them:

```sh
gh pr list --state merged -L 30 --json number,labels \
  -q '.[] | "\(.number)\t\([.labels[].name] | join(","))"' |
while IFS=$'\t' read -r n labels; do
  kind=$(pr "$n" | hunch pick --in whole "What kind of change is this pull request?" \
    -c feature,fix,refactor,docs,chore --tsv | cut -f1-2)
  printf '#%s\t%s\thuman: %s\n' "$n" "$kind" "$labels"
done
```

The second column is hunch's confidence. Where it disagrees with people, it's
usually low; that's the number to threshold on.

## 3. More questions, same call

Every question about a record goes into one API call, so asking about risk
and breaking changes adds almost no latency. Put the questions in a file:

```yaml
# .github/hunch/pr.yaml
context: A pull request to a Go CLI: title, description, then the diff.
questions:
  kind:
    type: choice
    q: What kind of change is this pull request?
    options:
      - feature=Adds new user-facing behavior
      - fix=Corrects broken behavior, without adding features
      - refactor=Restructures code, behavior stays the same
      - docs=Only documentation, comments or examples
      - chore=Dependencies, CI, build or tooling
  risk:
    type: score
    q: How risky is it to merge this change?
    levels:
      - low=Small, isolated, well covered by tests
      - medium=Touches shared code or changes behavior
      - high=Touches auth, payments, data migrations or many modules
  breaking:
    type: noul
    q: Does this change break compatibility for existing users, such as removed flags, renamed fields or changed defaults?
```

```sh
pr 42 | hunch ask -f .github/hunch/pr.yaml --in whole --json | jq .hunch
```

```json
{
  "kind": {"choice": "fix", "probs": {"feature": 0.03, "fix": 0.94, "refactor": 0.03, "docs": 0, "chore": 0}, "confidence": 0.9},
  "risk": {"score": 0.41, "level": "low", "probs": {"low": 0.6, "medium": 0.39, "high": 0.01}, "confidence": 0.55},
  "breaking": {"p": 0.04}
}
```

## 4. The workflow

Store your key as the `TYPESAFE_API_KEY` repository secret, create the labels
once (`gh label create fix`, `gh label create risk:high`, ...), and add:

```yaml
# .github/workflows/label.yml
name: Label pull requests
on:
  pull_request:
    types: [opened, reopened, ready_for_review]

permissions:
  contents: read
  pull-requests: write

jobs:
  label:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version: stable
      - run: go install github.com/yarolegovich/hunch@latest

      - name: Judge and label
        env:
          GH_TOKEN: ${{ github.token }}
          PR: ${{ github.event.pull_request.number }}
          TYPESAFE_API_KEY: ${{ secrets.TYPESAFE_API_KEY }}
        run: |
          { gh pr view "$PR" --json title,body -q '.title + "\n\n" + .body'; gh pr diff "$PR"; } \
            | hunch ask -f .github/hunch/pr.yaml --in whole --json > verdict.json

          labels=$(jq -r '.hunch
            | (if .kind.confidence >= 0.6 then .kind.choice else empty end),
              (if .risk.level == "high" then "risk:high" else empty end),
              (if .breaking.p >= 0.8 then "breaking" else empty end)' verdict.json | paste -sd, -)

          [ -n "$labels" ] && gh pr edit "$PR" --add-label "$labels"
          jq -c .hunch verdict.json >> "$GITHUB_STEP_SUMMARY"
```

A low-confidence `kind` gets no label instead of a wrong one, and the full
verdict lands in the job summary so you can see why.

## Notes

- **Cost.** A typical PR is around 2,000 tokens, so this is well under a
  hundredth of a cent per PR, three questions included.
- **Big diffs.** For very large PRs, add `--long-context`, or skip lock files
  and generated code: `gh pr diff "$PR" | filterdiff -x '*.lock' -x '*_gen.go'`.
- **Forks.** Secrets aren't available to `pull_request` runs from forks. Use
  `pull_request_target` only if you understand what it exposes.
- **Pinning.** Pin a model version (`--model jev-1.13.0`, see
  [hunch models](../docs/commands/models.md)) once you've tuned the thresholds,
  so a new release doesn't move them.
