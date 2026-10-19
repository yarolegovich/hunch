# hunch pick

Pick which of several choices fits each record. Uses a
[Choice](https://docs.typesafe.ai/primitives/choice) question.

```sh
hunch pick <question> -c a,b,c [file...]
```

## Choices

`-c` (`--choices`) is comma-separated or repeated. A choice can carry a
description, which sharpens the boundaries between them; a value with `=` is
not split on commas, so descriptions may contain them:

```sh
hunch pick "Which team should handle this?" \
  -c 'billing=Payments, invoices, refunds' \
  -c 'technical=Bugs, outages, integrations' \
  -c 'sales=Pricing, upgrades, new accounts'
```

Up to 255 choices.

## Output

```json
{"id":7,"body":"refund please","hunch":{"choice":"billing","probs":{"billing":0.88,"technical":0.12,"sales":0},"confidence":0.81}}
```

* `choice` is the most likely choice.
* `probs` has every choice's probability, in the order given.
* `confidence` is how certain the model is overall; see
  [Confidence](https://docs.typesafe.ai/confidence).

`--tsv` prints `choice⇥confidence⇥input`.

## Flags

| Flag | |
| - | - |
| `-c`, `--choices` | the choices (required) |
| `--only CHOICE` | keep records whose choice is `CHOICE` (repeatable) |
| `--min P`, `--max P` | keep records whose chosen choice's probability is in range |
| `--conf C` | drop answers with confidence below `C` |
| `--unsure FILE` | write records dropped by `--conf` to `FILE` |
| `-o FILE` | write to `FILE`; `{choice}` in the path writes one file per choice |
| `--sort`, `--top N` | sort by the chosen choice's probability; keep the first `N` |
| `--context TEXT`, `--context-from FILE` | guidance sent with the question |

Plus the [input, output and execution flags](../usage.md) shared by every
command.

## Routing

`-o 'out/{choice}.jsonl'` writes each record to the file for its choice. With
`--conf`, records below the threshold go to `out/_unsure.jsonl` instead, so a
person can look at them:

```sh
hunch pick "Which team should handle this?" -c billing,technical,sales \
  --jsonpath '$.body' --conf 0.6 -o 'out/{choice}.jsonl' < tickets.jsonl
wc -l out/*
```

## Examples

```sh
# only the billing tickets
hunch pick "Which team?" -c billing,technical,sales --only billing --raw < tickets.txt

# label distribution of a file
hunch pick "Language?" -c english,spanish,other --tsv posts.txt | cut -f1 | sort | uniq -c
```
