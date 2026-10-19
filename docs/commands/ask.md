# hunch ask

Ask several questions about each record, in one API call per record.

```sh
hunch ask -f questions.yaml [file...]
hunch @name [file...]                 # a question set saved as a preset
```

## The questions file

```yaml
context: Lines come from our payments API.      # optional, sent with every question
questions:
  auth:  { type: noul,   q: "Is this an authentication failure?" }
  sev:   { type: choice, q: "Severity?", options: [low, medium, "high=Customer-facing outage"] }
  noise: { type: score,  q: "How noisy is this line?", levels: [signal, mixed, noise] }
keep: "auth.p > 0.8 && sev.choice != 'low'"     # optional
```

* `type` is `noul` (yes/no), `choice` or `score`.
* `options` and `levels` items are `name`, `name=description` or
  `{name: description}`. Unlike flags, they are never split on commas.
* A noul question may set `yes_when:` and `no_when:`.

Questions are answered independently against the same record, so adding one
adds little latency. Keep each question narrow and combine the answers in
`keep` or downstream; that's where you control the weighting.

## Output

Answers go under the output key by question id, each in the same shape as
[yes](yes.md), [pick](pick.md) and [score](score.md):

```json
{"line":"...","hunch":{"auth":{"p":0.97},"sev":{"choice":"medium","probs":{"low":0.1,"medium":0.72,"high":0.18},"confidence":0.64},"noise":{"score":0.2,"level":"signal","probs":{"signal":0.8,"mixed":0.2,"noise":0},"confidence":0.7}}}
```

## Keep expressions

`keep` (or `--keep` on the command line, which overrides the file) is an
[expr](https://expr-lang.org) expression. Each question id holds the fields
hunch prints: `p`; `choice`, `probs`, `confidence`; `score`, `level`, `probs`,
`confidence`.

```sh
hunch ask -f triage.yaml --keep "sev.choice == 'high' || auth.p > 0.9" --raw app.log
```

## Flags

| Flag | |
| - | - |
| `-f`, `--file` | the questions file (required) |
| `--keep EXPR` | keep records for which `EXPR` is true |
| `--conf C`, `--unsure FILE` | drop (or set aside) records where any answer's confidence is below `C` |
| `--context TEXT`, `--context-from FILE` | guidance sent with every question; overrides the file's `context` |

Plus the [input, output and execution flags](../usage.md) shared by every
command. `--tsv` needs a single question, so it isn't available here.

## Presets

Save a question set in the [config file](../usage.md#configuration) and run it
by name:

```yaml
presets:
  triage:
    questions:
      auth: { type: noul, q: "Is this an authentication failure?" }
    keep: auth.p > 0.8
```

```sh
kubectl logs api-7f | hunch @triage --raw
```
