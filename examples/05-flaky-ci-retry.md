# 05 · Flaky CI retry

A job fails, you open the log, it's a registry timeout, you click "Re-run
failed jobs". This automates that: when CI fails, hunch reads the end of the
log and decides whether the environment broke or the code did. It re-runs
only the first kind.

You'll use `hunch pick` on the failed log as one record, and `gh run rerun`.

## 1. Try it on a failed run

```sh
gh run view 1234567890 --log-failed | tail -n 300 \
  | hunch pick --in whole "Why did this CI job fail?" -c infra,flaky,code
```

Short choice names leave the boundaries to guesswork. A test that timed out
waiting for a worker could be read as broken code. Describe each choice and
hunch sorts typical failures cleanly:

```sh
gh run view 1234567890 --log-failed | tail -n 300 \
  | hunch pick --in whole "Why did this CI job fail?" \
      -c 'infra=The CI environment broke: network errors, registry or service outages, runner out of disk or memory' \
      -c 'flaky=A test that depends on timing, ordering or shared state and may pass on a retry' \
      -c 'code=The code or tests are wrong: compile errors, failed assertions, lint errors'
```

| The log ends with | Answer |
| - | - |
| `npm ERR! code ECONNRESET ... socket hang up` | infra (1.00) |
| `write /var/lib/docker/tmp/layer.tar: no space left on device` | infra (1.00) |
| `TestWorkerShutdown: timed out waiting for worker to stop after 5s` | flaky (0.79) |
| `invoice_test.go:42: total = 118.00, want 120.00` | code (1.00) |
| `./handler.go:31:9: undefined: parseRequest` | code (1.00) |

## 2. Check it against your history

Run it over your last 30 failures and see what it would have retried:

```sh
gh run list --status failure -L 30 --json databaseId,displayTitle \
  -q '.[] | "\(.databaseId)\t\(.displayTitle)"' |
while IFS=$'\t' read -r id title; do
  cause=$(gh run view "$id" --log-failed | tail -n 300 \
    | hunch pick --in whole "Why did this CI job fail?" -c infra,flaky,code --tsv | cut -f1-2)
  printf '%s\t%s\t%s\n' "$id" "$cause" "$title"
done
```

## 3. The workflow

A second workflow runs whenever your `CI` workflow fails. It reads the log,
retries infra and flaky failures, and gives up after the third attempt so a
real outage doesn't loop forever:

```yaml
# .github/workflows/retry.yml
name: Retry flaky CI
on:
  workflow_run:
    workflows: [CI]
    types: [completed]

permissions:
  actions: write

jobs:
  retry:
    if: github.event.workflow_run.conclusion == 'failure' && github.event.workflow_run.run_attempt < 3
    runs-on: ubuntu-latest
    steps:
      - uses: actions/setup-go@v5
        with:
          go-version: stable
      - run: go install github.com/yarolegovich/hunch@latest

      - name: Retry if it wasn't the code
        env:
          GH_TOKEN: ${{ github.token }}
          GH_REPO: ${{ github.repository }}
          RUN: ${{ github.event.workflow_run.id }}
          TYPESAFE_API_KEY: ${{ secrets.TYPESAFE_API_KEY }}
        run: |
          cause=$(gh run view "$RUN" --log-failed | tail -n 300 \
            | hunch pick --in whole "Why did this CI job fail?" \
                -c 'infra=The CI environment broke: network errors, registry or service outages, runner out of disk or memory' \
                -c 'flaky=A test that depends on timing, ordering or shared state and may pass on a retry' \
                -c 'code=The code or tests are wrong: compile errors, failed assertions, lint errors' \
                --conf 0.7 --tsv | cut -f1) || true

          case "$cause" in
            infra|flaky)
              gh run rerun "$RUN" --failed
              echo "🔁 Re-ran failed jobs: looks like **$cause**." >> "$GITHUB_STEP_SUMMARY" ;;
            *)
              echo "Not retried: looks like **${cause:-unclear}**." >> "$GITHUB_STEP_SUMMARY" ;;
          esac
```

With `--conf 0.7`, an answer hunch isn't sure about prints nothing, so
`cause` is empty and the run isn't retried. When in doubt, a person looks.

## Notes

- **Cost.** 300 log lines is roughly 10,000 tokens: well under a tenth of a
  cent per failure.
- **Default branch.** `workflow_run` workflows only run from the default
  branch, so merge `retry.yml` before expecting it to fire on PRs.
- **Keep the evidence.** Retrying hides flaky tests. When the answer is
  `flaky`, also append the test name to an issue, so they get fixed instead of
  retried forever.
