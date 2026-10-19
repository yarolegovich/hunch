# hunch auth

Set up which provider hunch talks to, its API key and the default model.

```sh
hunch auth login      # guided setup
hunch auth status     # provider, model and key in use
hunch auth logout     # remove the provider's key from the keychain
```

## Providers

| Provider | `--provider` | Key | Default model |
| - | - | - | - |
| [TypeSafe](https://console.typesafe.ai/) | `typesafe` | `TYPESAFE_API_KEY` | `jev-latest` |
| [OpenRouter](https://openrouter.ai/settings/keys) | `openrouter` | `OPENROUTER_API_KEY` | `~typesafe/jev-latest` |
| [OpenCode Zen](https://opencode.ai/console) | `zen` | `OPENCODE_API_KEY` | `jev-1.13` (also `jev-1.13-free`) |

All three serve the same System One API, so every command works the same on
each.

## login

`hunch auth login` asks for:

1. **A provider**, with a link to where you create its key.
2. **The key**, typed without echo. If the provider's environment variable is
   set or a key is already stored, Enter reuses it.
3. **A model**, defaulting to the provider's default or your saved choice.

It then checks the key with one small request, stores the key in the OS
keychain (macOS Keychain, Secret Service, Windows Credential Manager), and
writes the provider and model to the [config file](../usage.md#configuration).
The rest of the config file, including comments, is kept.

Without a terminal nothing is asked: the key comes from stdin or the
provider's environment variable, and flags pick the rest.

```sh
pbpaste | hunch auth login --provider openrouter --model '~typesafe/jev-latest'
```

| Flag | |
| - | - |
| `--provider ID` | skip the provider question |
| `--model NAME` | skip the model question |
| `--no-verify` | save without checking the key |

## Where the key comes from

1. The provider's environment variable (`TYPESAFE_API_KEY`, `OPENROUTER_API_KEY`,
   `OPENCODE_API_KEY`).
2. The OS keychain, written by `hunch auth login`.

Keys are never written to the config file or printed; `status` shows only the
last four characters.

## Switching providers

`hunch auth login` again saves a different provider. For one run, pass
`--provider` (and optionally `--model`) to any command, or set
`HUNCH_PROVIDER`:

```sh
hunch "Is this spam?" --provider zen --model jev-1.13-free comments.txt
```
