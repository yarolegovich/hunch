# hunch

**Sort, filter and route text by what it means.** Pipe in logs, tickets,
reviews or diffs, ask a question in plain English, and `hunch` answers it for
every line: yes or no, which category, or how much. Each answer comes with a
confidence score your next command can act on.

```sh
kubectl logs api | hunch "Is this an authentication failure?" --min 0.8 --raw
```

![hunch demo](docs/demo/hunch-demo.gif)

Each record is judged by Jev, TypeSafe's
[System One](https://docs.typesafe.ai/introduction) model, through TypeSafe,
OpenRouter or OpenCode Zen. Answers are calibrated probabilities, so
thresholds and routing are built in.

Jev is fast and cheap enough to run on everything. Say hunch labels every pull
request in CI. A typical PR (title, description and a few hundred lines of
diff) is about 2,000 tokens. At $0.042 per million input tokens, with output
free, that's less than a hundredth of a cent per PR: **one dollar labels about
12,000 PRs**, each in roughly 100 ms. Further questions about the same PR,
such as how risky it is or whether it adds secrets, go into that same call and
add little latency.

<sub>TypeSafe [pricing](https://docs.typesafe.ai/models) as of October 19,
2026; OpenRouter and OpenCode Zen may differ.</sub>

## Install

```sh
brew install yarolegovich/tap/hunch
# or
go install github.com/yarolegovich/hunch@latest
```

Binaries are also on [Releases](https://github.com/yarolegovich/hunch/releases).
Then pick a provider and store its key:

```sh
hunch auth login
```

## Commands

| Command | What it asks |
| - | - |
| [`hunch yes <question>`](docs/commands/yes.md) | a yes/no question (also plain `hunch <question>`) |
| [`hunch pick <question> -c a,b,c`](docs/commands/pick.md) | which of several choices fits |
| [`hunch score <question> -l low,mid,high`](docs/commands/score.md) | where it falls on ordered levels |
| [`hunch ask -f questions.yaml`](docs/commands/ask.md) | several questions at once, in one call |
| [`hunch auth`](docs/commands/auth.md) | set up the provider, key and model |
| [`hunch models`](docs/commands/models.md) | list the provider's models |

Input modes, output formats, filtering, exit codes and configuration are in
[docs/usage.md](docs/usage.md).

## Examples

```sh
# keep only auth failures
kubectl logs api-7f | hunch "Is this an authentication failure?" --min 0.8 --raw

# judge one field of each JSON record, keep the rest for later steps
hunch "Is the customer asking for a refund?" --jsonpath '$.body' --min 0.8 < tickets.jsonl | jq -r .id

# route support tickets into a file per team, low-confidence ones aside
hunch pick "Which team should handle this?" -c billing,technical,sales \
  --jsonpath '$.body' --conf 0.6 -o 'out/{choice}.jsonl' < tickets.jsonl

# top 10 angriest reviews
hunch score "How frustrated is the customer?" -l calm,frustrated,furious --top 10 reviews.txt

# label a pull request in CI
gh pr diff "$PR" | hunch pick --in whole "What kind of change is this?" -c feature,fix,refactor,docs --tsv \
  | cut -f1 | xargs -I{} gh pr edit "$PR" --add-label {}

# gate a commit on the staged diff (exit 1 when the answer is likely yes)
git diff --cached | hunch --in whole "Does this diff add debug code or secrets?" --max 0.3 >/dev/null
```

## License

[Apache-2.0](LICENSE)
