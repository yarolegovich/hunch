package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/yarolegovich/hunch/internal/provider"
)

// options holds every flag. Commands register the subset that applies to them.
type options struct {
	// input
	in       string
	jsonpath string

	// output
	key     string
	raw     bool
	tsv     bool
	jsonOut bool

	// execution
	provider    string
	model       string
	jobs        int
	retries     int
	unordered   bool
	keepGoing   bool
	dryRun      bool
	stats       bool
	longContext bool

	// question
	context     string
	contextFrom string
	yesWhen     string
	noWhen      string
	options     []string
	levels      []string
	file        string
	keep        string

	// selection
	min, max       float64
	hasMin, hasMax bool
	only           []string
	conf           float64
	unsure         string
	output         string
	sort           bool
	top            int

	cfg *config
}

func newOptions(cfg *config, e *env) *options {
	o := &options{
		key:      "hunch",
		provider: provider.TypeSafe.ID,
		jobs:     8,
		retries:  4,
		cfg:      cfg,
	}
	cfg.applyDefaults(o)
	applyEnv(o, e)
	return o
}

func addIOFlags(cmd *cobra.Command, o *options) {
	f := cmd.Flags()
	f.StringVar(&o.in, "in", "auto", "input mode: auto, lines, jsonl or whole")
	f.StringVar(&o.jsonpath, "jsonpath", "", "with JSONL input, judge only the value at this `path` (e.g. $.body); the whole record is still printed")
	f.StringVar(&o.key, "key", o.key, "output field that holds the answers")
	f.BoolVar(&o.raw, "raw", false, "print only the original input of kept records")
	f.BoolVar(&o.tsv, "tsv", false, "print value<TAB>input lines for sort and awk")
	f.BoolVar(&o.jsonOut, "json", false, "print JSONL even when stdout is a terminal")
	f.StringVarP(&o.output, "output", "o", "", "write kept records to this `file`; pick and score split by answer with {choice} or {level} in the path")
	addProviderFlags(cmd, o)
	f.IntVarP(&o.jobs, "jobs", "j", o.jobs, "concurrent requests")
	f.IntVar(&o.retries, "retries", o.retries, "retries on 408, 429, 5xx and connection errors")
	f.BoolVar(&o.unordered, "unordered", false, "print results as they finish instead of in input order")
	f.BoolVarP(&o.keepGoing, "keep-going", "k", false, "report failed records on stderr and continue")
	f.BoolVar(&o.dryRun, "dry-run", false, "print the request for the first record and exit")
	f.BoolVar(&o.stats, "stats", false, "print records, calls, tokens and wall time to stderr")
	f.BoolVar(&o.longContext, "long-context", false, "raise context limits to 262k tokens (slower, less accurate)")
	f.StringVar(&o.context, "context", "", "guidance sent with every question")
	f.StringVar(&o.contextFrom, "context-from", "", "read --context from this `file`")
}

func addProviderFlags(cmd *cobra.Command, o *options) {
	f := cmd.Flags()
	f.StringVar(&o.provider, "provider", o.provider, "API provider: "+strings.Join(provider.IDs(), ", "))
	f.StringVar(&o.model, "model", o.model, "model (default: the provider's configured model)")
	_ = cmd.RegisterFlagCompletionFunc("provider", func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
		return provider.IDs(), cobra.ShellCompDirectiveNoFileComp
	})
}

// endpoint resolves the provider, model, base URL and, if needKey, the API
// key for this run.
func (o *options) endpoint(e *env, needKey bool) (*provider.Endpoint, error) {
	p, err := provider.Lookup(o.provider)
	if err != nil {
		return nil, usageErrorf("%v", err)
	}
	pc := o.cfg.Providers[p.ID]
	ep := &provider.Endpoint{Provider: p, Model: o.model, URL: pc.BaseURL}
	if ep.Model == "" {
		ep.Model = pc.Model
	}
	if ep.Model == "" {
		ep.Model = p.DefaultModel()
	}
	if u := e.getenv("HUNCH_BASE_URL"); u != "" {
		ep.URL = u
	}
	if needKey {
		if ep.Key, _, err = apiKey(e, p); err != nil {
			return nil, err
		}
	}
	return ep, nil
}

func addRangeFlags(cmd *cobra.Command, o *options) {
	f := cmd.Flags()
	f.Float64Var(&o.min, "min", 0, "keep records whose value is at least this")
	f.Float64Var(&o.max, "max", 0, "keep records whose value is at most this")
	f.BoolVar(&o.sort, "sort", false, "buffer all input and sort by value, highest first")
	f.IntVar(&o.top, "top", 0, "keep only the first N records after sorting (implies --sort)")
}

func addConfFlags(cmd *cobra.Command, o *options) {
	f := cmd.Flags()
	f.Float64Var(&o.conf, "conf", 0, "drop answers whose confidence is below this")
	f.StringVar(&o.unsure, "unsure", "", "write records dropped by --conf to this file instead")
}

func addNoulFlags(cmd *cobra.Command, o *options) {
	f := cmd.Flags()
	f.StringVar(&o.yesWhen, "yes-when", "", "what makes the answer yes")
	f.StringVar(&o.noWhen, "no-when", "", "what makes the answer no")
}

// resolve records which optional flags were set and checks the common ones.
func (o *options) resolve(cmd *cobra.Command) error {
	f := cmd.Flags()
	o.hasMin = f.Lookup("min") != nil && f.Changed("min")
	o.hasMax = f.Lookup("max") != nil && f.Changed("max")
	if o.top > 0 {
		o.sort = true
	}
	switch o.in {
	case "auto", "lines", "jsonl", "whole":
	default:
		return usageErrorf("--in must be auto, lines, jsonl or whole, not %q", o.in)
	}
	if o.jsonpath != "" && o.in != "auto" && o.in != "jsonl" {
		return usageErrorf("--jsonpath needs JSONL input")
	}
	if n := countTrue(o.raw, o.tsv, o.jsonOut); n > 1 {
		return usageErrorf("--raw, --tsv and --json are mutually exclusive")
	}
	if o.jobs < 1 {
		return usageErrorf("--jobs must be at least 1")
	}
	if o.retries < 0 {
		return usageErrorf("--retries can't be negative")
	}
	if o.key == "" {
		return usageErrorf("--key can't be empty")
	}
	if o.contextFrom != "" {
		if o.context != "" {
			return usageErrorf("--context and --context-from are mutually exclusive")
		}
		b, err := os.ReadFile(o.contextFrom)
		if err != nil {
			return &exitError{exitUsage, err}
		}
		o.context = strings.TrimSpace(string(b))
	}
	return nil
}

func countTrue(bs ...bool) int {
	n := 0
	for _, b := range bs {
		if b {
			n++
		}
	}
	return n
}

// parseLabels turns -o/-l values into labels. Each value is split on commas
// unless it carries a description ("name=description"), so descriptions may
// contain commas: -o 'billing=Payments, refunds' -o 'tech=Bugs'.
func parseLabels(values []string) ([]label, error) {
	var out []label
	seen := map[string]bool{}
	for _, v := range values {
		parts := []string{v}
		if !strings.Contains(v, "=") {
			parts = strings.Split(v, ",")
		}
		for _, p := range parts {
			name, desc, _ := strings.Cut(p, "=")
			name = strings.TrimSpace(name)
			if name == "" {
				return nil, fmt.Errorf("empty name in %q", v)
			}
			if seen[name] {
				return nil, fmt.Errorf("duplicate %q", name)
			}
			seen[name] = true
			out = append(out, label{name: name, desc: strings.TrimSpace(desc)})
		}
	}
	return out, nil
}
