# hunch score

Rate each record on ordered levels, lowest first. Uses a
[Score](https://docs.typesafe.ai/primitives/score) question.

```sh
hunch score <question> -l low,mid,high [file...]
```

## Levels

`-l` (`--levels`) works like `pick`'s `-c`: comma-separated or repeated, with
optional descriptions (`-l 'calm=Polite, patient'`). Between 2 and 10 levels.

## Output

```json
{"line":"I have emailed support FOUR times","hunch":{"score":1.96,"level":"furious","probs":{"calm":0,"annoyed":0.04,"furious":0.96},"confidence":0.92}}
```

* `score` is the probability-weighted level index, from 0 (the first level) to
  the number of levels minus one. It can land between levels, which makes it
  good for sorting.
* `level` is the level nearest to `score`.
* `probs` has each level's probability; `confidence` is how certain the model
  is overall.

`--tsv` prints `score⇥level⇥input`.

## Flags

| Flag | |
| - | - |
| `-l`, `--levels` | the levels, lowest first (required) |
| `--min S`, `--max S` | keep records whose `score` is in range |
| `--sort`, `--top N` | sort by `score`, highest first; keep the first `N` |
| `--conf C`, `--unsure FILE` | drop (or set aside) answers with confidence below `C` |
| `-o FILE` | write to `FILE`; `{level}` in the path writes one file per level |
| `--context TEXT`, `--context-from FILE` | guidance sent with the question |

Plus the [input, output and execution flags](../usage.md) shared by every
command.

## Examples

```sh
# top 10 angriest reviews
hunch score "How frustrated is the customer?" -l calm,frustrated,furious --top 10 reviews.txt

# anything at least moderately urgent (score is 0..2 here)
hunch score "How urgent is this?" -l low,medium,high --min 1 --raw < alerts.log

# one file per mood
hunch score "Mood?" -l negative,neutral,positive -o 'by-mood/{level}.jsonl' reviews.txt
```
