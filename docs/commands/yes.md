# hunch yes

Ask a yes/no question about each record. Uses a
[Noul](https://docs.typesafe.ai/primitives/noul) question.

```sh
hunch yes <question> [file...]
hunch <question> [file...]          # same thing
```

## Output

Each record gets `p`, the probability that the answer is yes:

```json
{"line":"POST /api/login 401 user=42","hunch":{"p":0.97}}
```

In a terminal, answers are worded `yes` (p ≥ 0.70), `maybe` or `no`
(p ≤ 0.30), with `p` in parentheses. The wording is only for display;
`--min` and `--max` compare `p`.

`--tsv` prints `p⇥input`.

## Flags

| Flag | |
| - | - |
| `--min P`, `--max P` | keep records whose `p` is in range |
| `--sort`, `--top N` | sort by `p`, highest first; keep the first `N` |
| `--yes-when TEXT` | what makes the answer yes |
| `--no-when TEXT` | what makes the answer no |
| `--context TEXT`, `--context-from FILE` | guidance sent with the question |

Plus the [input, output and execution flags](../usage.md) shared by every
command.

`--yes-when` and `--no-when` pin down a subtle boundary:

```sh
hunch "Is this spam?" \
  --yes-when "Unsolicited promotion or links from an unknown sender" \
  --no-when "Messages from customers, even if they mention products" \
  --sort --top 20 comments.txt
```

## Examples

```sh
# keep only auth failures, print the lines like a filter
kubectl logs api-7f | hunch "Is this an authentication failure?" --min 0.8 --raw

# judge each file as a whole; prints the names of the files that match
hunch --in whole "Does this file contain hard-coded credentials?" --min 0.7 --raw src/*.go

# gate a commit on the staged diff
git diff --cached | hunch --in whole "Does this diff add debug code or secrets?" --max 0.3 >/dev/null
```
