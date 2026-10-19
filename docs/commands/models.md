# hunch models

List the System One models the current provider offers.

```sh
hunch models
hunch models --provider openrouter
hunch models --json
```

It prints each model's name, release date and description. The list comes
from the provider, filtered to Jev models.

Any listed name can be passed to `--model` or saved with
[`hunch auth login`](auth.md). Aliases such as `jev-latest` move when a new
release ships; pin a versioned id (e.g. `jev-1.13.0`) if you've tuned
thresholds against one version. See [Models](https://docs.typesafe.ai/models).
