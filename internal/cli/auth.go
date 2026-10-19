package cli

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	"github.com/zalando/go-keyring"
	"golang.org/x/term"

	"github.com/yarolegovich/hunch/internal/provider"
	"github.com/yarolegovich/hunch/internal/typesafe"
)

const keyringService = "hunch"

func keyringUser(p *provider.Provider) string { return p.ID + "-api-key" }

// apiKey finds the provider's API key: its environment variable, then the OS
// keychain. It returns where the key came from.
func apiKey(e *env, p *provider.Provider) (key, from string, err error) {
	if k := strings.TrimSpace(e.getenv(p.KeyEnv)); k != "" {
		return k, p.KeyEnv, nil
	}
	if k, err := keyring.Get(keyringService, keyringUser(p)); err == nil && k != "" {
		return k, "keychain", nil
	}
	return "", "", &exitError{exitAuth, fmt.Errorf("no %s API key: set %s or run 'hunch auth login'", p.Name, p.KeyEnv)}
}

// apiExit maps a client error to an exit code.
func apiExit(err error) error {
	if typesafe.IsAuthError(err) {
		return &exitError{exitAuth, err}
	}
	return &exitError{exitAPI, err}
}

func newAuthCmd(e *env, o *options) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "auth",
		Short: "Set up the API provider and key",
		Long: `hunch sends requests to one of these System One providers:

` + providerList() + `
The key is read from the provider's environment variable, or else from the OS
keychain (macOS Keychain, Secret Service, Windows Credential Manager). It is
never written to the config file or printed.`,
	}
	cmd.AddCommand(newLoginCmd(e, o), newLogoutCmd(e, o), newStatusCmd(e, o))
	return cmd
}

func providerList() string {
	var b strings.Builder
	for _, p := range provider.All() {
		fmt.Fprintf(&b, "  %-11s %-13s key in %s\n", p.ID, p.Name, p.KeyEnv)
	}
	return b.String()
}

func newLoginCmd(e *env, o *options) *cobra.Command {
	var (
		providerID string
		model      string
		noVerify   bool
	)
	cmd := &cobra.Command{
		Use:   "login",
		Short: "Choose a provider, store its key and save the choice",
		Long: `Walk through choosing a provider, entering its API key and picking a model.
The key is checked with one small request, stored in the OS keychain, and the
provider and model are saved to the config file, so later runs need no flags.

Without a terminal, nothing is asked: the key is read from stdin (or the
provider's environment variable) and flags pick the rest:

  pbpaste | hunch auth login --provider openrouter`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			l := &login{e: e, cfg: o.cfg, in: bufio.NewReader(e.stdin)}
			return l.run(cmd, providerID, model, noVerify)
		},
	}
	f := cmd.Flags()
	f.StringVar(&providerID, "provider", "", "provider: "+strings.Join(provider.IDs(), ", "))
	f.StringVar(&model, "model", "", "model to save as the default")
	f.BoolVar(&noVerify, "no-verify", false, "save without checking the key against the API")
	_ = cmd.RegisterFlagCompletionFunc("provider", func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
		return provider.IDs(), cobra.ShellCompDirectiveNoFileComp
	})
	return cmd
}

type login struct {
	e   *env
	cfg *config
	in  *bufio.Reader
}

func (l *login) interactive() bool { return l.e.interactive() }

func (l *login) say(format string, args ...any) { fmt.Fprintf(l.e.stderr, format, args...) }

func (l *login) run(cmd *cobra.Command, providerID, model string, noVerify bool) error {
	p, err := l.chooseProvider(providerID)
	if err != nil {
		return err
	}
	key, store, err := l.chooseKey(p)
	if err != nil {
		return err
	}
	if model == "" {
		if model, err = l.chooseModel(p); err != nil {
			return err
		}
	}

	ep := &provider.Endpoint{Provider: p, Key: key, Model: model, URL: l.cfg.Providers[p.ID].BaseURL}
	if u := l.e.getenv("HUNCH_BASE_URL"); u != "" {
		ep.URL = u
	}
	if !noVerify {
		l.say("Checking the key with %s (%s)... ", p.Name, model)
		res, err := ep.Client().Ask(cmd.Context(), typesafe.Request{
			State:     "hunch auth login",
			Questions: typesafe.Questions{"ok": typesafe.Noul("Is this a connectivity check?")},
		})
		if err != nil {
			l.say("failed\n")
			return apiExit(err)
		}
		l.say("ok, %s answered\n", res.Model)
	}

	if store {
		if err := keyring.Set(keyringService, keyringUser(p), key); err != nil {
			return usageErrorf("storing key in the keychain: %v", err)
		}
		l.say("Stored key %s in the keychain.\n", mask(key))
	}
	if err := l.cfg.saveProvider(p.ID, model); err != nil {
		return usageErrorf("saving config: %v", err)
	}
	l.say("Saved provider %s and model %s to %s.\n", p.ID, model, l.cfg.path)
	if l.interactive() {
		l.say("\nTry it:\n  echo 'I was charged twice' | hunch \"Is this about billing?\"\n")
	}
	return nil
}

func (l *login) chooseProvider(id string) (*provider.Provider, error) {
	if id != "" {
		p, err := provider.Lookup(id)
		if err != nil {
			return nil, usageErrorf("%v", err)
		}
		return p, nil
	}
	current := provider.TypeSafe
	if p, err := provider.Lookup(l.cfg.Provider); err == nil {
		current = p
	}
	if !l.interactive() {
		return current, nil
	}
	l.say("Which provider do you want to use?\n")
	def := 0
	for i, p := range provider.All() {
		marker := " "
		if p == current {
			marker, def = "*", i
		}
		l.say(" %s %d) %-13s %s\n", marker, i+1, p.Name, p.KeyURL)
	}
	for range 3 {
		ans, err := l.line(fmt.Sprintf("Provider [%d]: ", def+1))
		if err != nil {
			return nil, err
		}
		if ans == "" {
			return provider.All()[def], nil
		}
		if n, err := strconv.Atoi(ans); err == nil && n >= 1 && n <= len(provider.All()) {
			return provider.All()[n-1], nil
		}
		if p, err := provider.Lookup(strings.ToLower(ans)); err == nil {
			return p, nil
		}
		l.say("Enter a number from 1 to %d.\n", len(provider.All()))
	}
	return nil, usageErrorf("no provider chosen")
}

// chooseKey returns the key and whether it should be stored in the keychain.
// A key that already lives in the environment or the keychain is not stored
// again.
func (l *login) chooseKey(p *provider.Provider) (key string, store bool, err error) {
	envKey := strings.TrimSpace(l.e.getenv(p.KeyEnv))
	stored, _ := keyring.Get(keyringService, keyringUser(p))

	var typed string
	if l.interactive() {
		hint := ""
		switch {
		case envKey != "":
			hint = fmt.Sprintf(" (Enter to use $%s)", p.KeyEnv)
		case stored != "":
			hint = fmt.Sprintf(" (Enter to keep %s)", mask(stored))
		default:
			l.say("Create a key at %s\n", p.KeyURL)
		}
		if typed, err = l.secret(fmt.Sprintf("%s API key%s: ", p.Name, hint)); err != nil {
			return "", false, err
		}
	} else if typed, err = l.line(""); err != nil {
		return "", false, err
	}

	switch {
	case typed != "":
		return typed, true, nil
	case envKey != "":
		return envKey, false, nil
	case stored != "":
		return stored, false, nil
	}
	return "", false, usageErrorf("no key given; create one at %s", p.KeyURL)
}

func (l *login) chooseModel(p *provider.Provider) (string, error) {
	def := l.cfg.Providers[p.ID].Model
	if def == "" {
		def = p.DefaultModel()
	}
	if !l.interactive() {
		return def, nil
	}
	var others []string
	for _, m := range p.Models {
		if m != def {
			others = append(others, m)
		}
	}
	hint := ""
	if len(others) > 0 {
		hint = " (also: " + strings.Join(others, ", ") + ")"
	}
	ans, err := l.line(fmt.Sprintf("Model [%s]%s: ", def, hint))
	if err != nil || ans == "" {
		return def, err
	}
	return ans, nil
}

func (l *login) line(prompt string) (string, error) {
	if prompt != "" {
		l.say("%s", prompt)
	}
	s, err := l.in.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	if errors.Is(err, io.EOF) && s == "" && prompt != "" {
		return "", usageErrorf("input ended")
	}
	return strings.TrimSpace(s), nil
}

// secret reads without echo from a real terminal.
func (l *login) secret(prompt string) (string, error) {
	f, ok := l.e.stdin.(*os.File)
	if !ok || !term.IsTerminal(int(f.Fd())) {
		return l.line(prompt)
	}
	l.say("%s", prompt)
	b, err := term.ReadPassword(int(f.Fd()))
	l.say("\n")
	return strings.TrimSpace(string(b)), err
}

func newLogoutCmd(e *env, o *options) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "logout",
		Short: "Remove the provider's key from the OS keychain",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			p, err := provider.Lookup(o.provider)
			if err != nil {
				return usageErrorf("%v", err)
			}
			err = keyring.Delete(keyringService, keyringUser(p))
			if err != nil && !errors.Is(err, keyring.ErrNotFound) {
				return usageErrorf("removing key: %v", err)
			}
			fmt.Fprintf(e.stderr, "Removed the %s key from the keychain.\n", p.Name)
			if e.getenv(p.KeyEnv) != "" {
				fmt.Fprintf(e.stderr, "%s is still set in the environment.\n", p.KeyEnv)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&o.provider, "provider", o.provider, "provider: "+strings.Join(provider.IDs(), ", "))
	return cmd
}

func newStatusCmd(e *env, o *options) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show the provider, model and key hunch would use",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			ep, err := o.endpoint(e, false)
			if err != nil {
				return err
			}
			fmt.Fprintf(e.stdout, "Provider  %s (%s)\n", ep.ID, ep.Name)
			fmt.Fprintf(e.stdout, "Model     %s\n", ep.Model)
			fmt.Fprintf(e.stdout, "Config    %s\n", o.cfg.path)
			key, from, err := apiKey(e, ep.Provider)
			if err != nil {
				fmt.Fprintf(e.stdout, "Key       none (run 'hunch auth login')\n")
				return silentExit(exitAuth)
			}
			fmt.Fprintf(e.stdout, "Key       %s from %s\n", mask(key), from)
			return nil
		},
	}
	addProviderFlags(cmd, o)
	return cmd
}

// mask shows only the end of a key.
func mask(key string) string {
	if len(key) <= 8 {
		return "****"
	}
	return "…" + key[len(key)-4:]
}
