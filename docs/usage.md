# Using hunch

What every command shares: input, output, filtering, exit codes, execution and
configuration. For each command's own flags see [docs/commands](commands/).

## Input

| Mode | `--in` | Each record is |
| - | - | - |
| lines | `lines` | a non-empty line, sent as a string |
| JSONL | `jsonl` | a JSON value on one line, sent as structured data |
| whole | `whole` | a whole input (stdin, or each file), e.g. a file or a diff |

Without `--in`, the mode is detected from the first line: JSON means JSONL,
anything else means lines. File arguments work like `cat`; `-` is stdin.

### Judging one field: `--jsonpath`

With JSONL input, `--jsonpath` sends only one value of each record to the
model, but still prints the whole record with the answer added. Ids and
metadata stay available to later steps without influencing the judgment:

```sh
# {"id":101,"user":"u_42","created":"2026-10-01","body":"I was charged twice, refund please."}
hunch "Is the customer asking for a refund?" --jsonpath '$.body' --min 0.8 < tickets.jsonl | jq -r .id
```

Paths select a single value: `$.body`, `$.message.text`, `$.comments[0].body`,
`$['first name']`. The leading `$` is optional (`.body` works too). Wildcards,
`..` and filters are rejected because they can match many values. Quote the
path in single quotes so the shell leaves the `$` alone. A record without the
path is an error (or is skipped and reported with `-k`).

## Output

| Where | Default format |
| - | - |
| a terminal | a compact view: `yes (0.97)`, `billing 0.88`, `1.96 furious`, then the input |
| a pipe or file | JSONL: the original record plus the answers under `hunch` |

```json
{"line":"POST /api/login 401 user=42","hunch":{"p":0.97}}
{"id":101,"body":"refund please","hunch":{"choice":"billing","probs":{"billing":0.88,"technical":0.12,"sales":0},"confidence":0.81}}
```

Lines become `{"line": ...}`, whole inputs `{"file": ..., "text": ...}`, and
JSON values that aren't objects `{"value": ...}`. JSON objects keep their
fields and order.

| Flag | |
| - | - |
| `--raw` | print only the original input of kept records (file names in whole mode with file arguments) |
| `--tsv` | `value⇥input` lines for `sort`, `cut` and `awk` (see each command for the columns) |
| `--json` | JSONL even on a terminal |
| `-o FILE` | write to `FILE` instead of stdout; `pick` and `score` can [split by answer](commands/pick.md#routing) |
| `--key NAME` | put the answers under `NAME` instead of `hunch` |
| `--unordered` | print results as they finish instead of in input order |

## Filtering

| Flag | Commands | Keeps records where |
| - | - | - |
| `--min V`, `--max V` | yes, pick, score | the value is in range: `p`, the chosen choice's probability, or `score` |
| `--only CHOICE` | pick | the choice is `CHOICE` |
| `--conf C` | pick, score, ask | confidence is at least `C`; `--unsure FILE` keeps the rest aside |
| `--keep EXPR` | ask | an [expr](https://expr-lang.org) expression is true |
| `--sort`, `--top N` | yes, pick, score | (all input is buffered and sorted by value, highest first) |

## Exit codes

| Code | Meaning |
| - | - |
| 0 | at least one record kept |
| 1 | no records kept |
| 2 | usage, config or input error |
| 3 | missing or rejected API key (run `hunch auth login`) |
| 4 | API error after retries |
| 5 | some records failed and the rest were printed (`-k`) |

So `hunch ... >/dev/null` works as a condition in scripts and git hooks.

## Execution

| Flag | |
| - | - |
| `-j N` | concurrent requests (default 8) |
| `--retries N` | retries on 408, 429, 5xx and connection errors, with backoff (default 4) |
| `-k`, `--keep-going` | report failed records on stderr and continue |
| `--provider ID`, `--model NAME` | override the [saved provider and model](commands/auth.md) |
| `--long-context` | raise the context limit to 262k tokens (slower, less accurate) |
| `--dry-run` | print the request for the first record and exit |
| `--stats` | print records, calls, tokens and wall time to stderr |

All questions about a record go in one API call. Request bodies of 1 KiB or
more are zstd-compressed, and records over the API's 32 MiB limit fail without
being sent.

## Configuration

Settings come from flags, then environment variables, then the config file,
then defaults. The config file is `~/.config/hunch/config.yaml`
(`$XDG_CONFIG_HOME/hunch/config.yaml`, or `$HUNCH_CONFIG`).

```yaml
provider: openrouter            # $HUNCH_PROVIDER; written by hunch auth login
providers:
  openrouter:
    model: ~typesafe/jev-latest # $HUNCH_MODEL; written by hunch auth login
    # base_url: ...             # $HUNCH_BASE_URL
jobs: 8                         # $HUNCH_JOBS
retries: 4                      # $HUNCH_RETRIES
key: hunch                      # output field
presets:                        # run with: hunch @triage
  triage:
    questions:
      auth: { type: noul, q: "Is this an authentication failure?" }
    keep: auth.p > 0.8
```

API keys are never read from the config file; see [hunch auth](commands/auth.md).
