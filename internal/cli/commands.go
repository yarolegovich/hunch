package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/yarolegovich/hunch/internal/typesafe"
)

func newYesCmd(e *env, o *options) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "yes <question> [file...]",
		Short: "Ask a yes/no question; adds p, the probability of yes",
		Example: `  kubectl logs api-7f | hunch yes "Is this an authentication failure?" --min 0.8 --raw
  hunch yes "Is this spam?" --yes-when "Unsolicited promotion" --sort --top 20 comments.txt
  hunch yes "Asking for a refund?" --jsonpath '$.body' --min 0.8 < tickets.jsonl | jq -r .id`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error { return runYes(cmd, e, o, args) },
	}
	addIOFlags(cmd, o)
	addRangeFlags(cmd, o)
	addNoulFlags(cmd, o)
	return cmd
}

func runYes(cmd *cobra.Command, e *env, o *options, args []string) error {
	if err := o.resolve(cmd); err != nil {
		return err
	}
	s := &spec{single: true, context: o.context, questions: []question{{
		id: "q", kind: typesafe.TypeNoul, text: args[0], yes: o.yesWhen, no: o.noWhen,
	}}}
	return execute(cmd.Context(), e, o, s, args[1:])
}

func newPickCmd(e *env, o *options) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "pick <question> -c a,b,c [file...]",
		Short: "Pick one option; adds choice, probs and confidence",
		Long: `Pick one option from a list. Options may carry a description:
-c 'billing=Payments, refunds' -c 'technical=Bugs, outages'. A value without
"=" is split on commas. Up to 255 options.

--min and --max compare the chosen option's probability. -o 'out/{choice}.jsonl'
writes one file per option; with --conf, unsure records go to
out/_unsure.jsonl.`,
		Example: `  hunch pick "Which team should handle this?" -c billing,technical,sales \
    --jsonpath '$.body' --conf 0.6 -o 'out/{choice}.jsonl' < tickets.jsonl`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := o.resolve(cmd); err != nil {
				return err
			}
			labels, err := parseLabels(o.options)
			if err != nil {
				return usageErrorf("--choices: %v", err)
			}
			for _, only := range o.only {
				if !slices.ContainsFunc(labels, func(l label) bool { return l.name == only }) {
					return usageErrorf("--only %q is not one of the options", only)
				}
			}
			if err := checkConf(o); err != nil {
				return err
			}
			s := &spec{single: true, context: o.context, questions: []question{{
				id: "q", kind: typesafe.TypeChoice, text: args[0], labels: labels,
			}}}
			if err := s.validate(); err != nil {
				return usageErrorf("%v", err)
			}
			return execute(cmd.Context(), e, o, s, args[1:])
		},
	}
	addIOFlags(cmd, o)
	addRangeFlags(cmd, o)
	addConfFlags(cmd, o)
	cmd.Flags().StringArrayVarP(&o.options, "choices", "c", nil, "choices, comma-separated or repeated (name or name=description)")
	cmd.Flags().StringArrayVar(&o.only, "only", nil, "keep records whose choice is this option (repeatable)")
	_ = cmd.MarkFlagRequired("choices")
	return cmd
}

func newScoreCmd(e *env, o *options) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "score <question> -l low,mid,high [file...]",
		Short: "Rate on ordered levels; adds score, level, probs and confidence",
		Long: `Rate each record on ordered levels, lowest first. The score is the
probability-weighted level index (0 for the first level) and can land between
levels; level is the nearest one. Levels take descriptions like options do.
Up to 10 levels. -o 'out/{level}.jsonl' writes one file per level.

--min and --max compare the score.`,
		Example: `  hunch score "How frustrated is the customer?" -l calm,frustrated,furious --top 10 reviews.txt`,
		Args:    cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := o.resolve(cmd); err != nil {
				return err
			}
			labels, err := parseLabels(o.levels)
			if err != nil {
				return usageErrorf("-l: %v", err)
			}
			if err := checkConf(o); err != nil {
				return err
			}
			s := &spec{single: true, context: o.context, questions: []question{{
				id: "q", kind: typesafe.TypeScore, text: args[0], labels: labels,
			}}}
			if err := s.validate(); err != nil {
				return usageErrorf("%v", err)
			}
			return execute(cmd.Context(), e, o, s, args[1:])
		},
	}
	addIOFlags(cmd, o)
	addRangeFlags(cmd, o)
	addConfFlags(cmd, o)
	cmd.Flags().StringArrayVarP(&o.levels, "levels", "l", nil, "levels, lowest first, comma-separated or repeated")
	_ = cmd.MarkFlagRequired("levels")
	return cmd
}

func newAskCmd(e *env, o *options) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ask -f questions.yaml [file...]",
		Short: "Ask several questions per record in one call",
		Long: `Ask every question in a YAML file about each record, in one API call per
record. Answers are written under the output key by question id.

  context: Lines come from our payments API.     # optional
  questions:
    auth:  { type: noul,   q: "Is this an authentication failure?" }
    sev:   { type: choice, q: "Severity?", options: [low, medium, "high=Customer-facing outage"] }
    noise: { type: score,  q: "How noisy is this line?", levels: [signal, mixed, noise] }
  keep: "auth.p > 0.8 && sev.choice != 'low'"   # optional

A noul question may also set yes_when: and no_when:. The keep expression
(https://expr-lang.org) sees each question id with the fields hunch prints:
p; choice, probs, confidence; score, level, probs, confidence.

The same shape can be saved as a preset in the config file and run as
"hunch @name".`,
		Example: `  hunch ask -f triage.yaml app.log
  hunch ask -f triage.yaml --keep "sev.choice == 'high'" --raw < app.log`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := o.resolve(cmd); err != nil {
				return err
			}
			if o.tsv {
				return usageErrorf("--tsv needs a single question; use JSONL with ask")
			}
			if err := checkConf(o); err != nil {
				return err
			}
			data, err := os.ReadFile(o.file)
			if err != nil {
				return &exitError{exitUsage, err}
			}
			var qs questionSet
			if err := yaml.Unmarshal(data, &qs); err != nil {
				return usageErrorf("%s: %v", o.file, err)
			}
			s, err := qs.spec(o)
			if err != nil {
				return usageErrorf("%s: %v", o.file, err)
			}
			return execute(cmd.Context(), e, o, s, args)
		},
	}
	addIOFlags(cmd, o)
	addConfFlags(cmd, o)
	cmd.Flags().StringVarP(&o.file, "file", "f", "", "questions file")
	cmd.Flags().StringVar(&o.keep, "keep", "", "keep records for which this expression is true (overrides the file's keep)")
	_ = cmd.MarkFlagRequired("file")
	return cmd
}

func checkConf(o *options) error {
	if o.conf < 0 || o.conf > 1 {
		return usageErrorf("--conf must be between 0 and 1")
	}
	if o.unsure != "" && o.conf == 0 {
		return usageErrorf("--unsure needs --conf")
	}
	return nil
}

func newModelsCmd(e *env, o *options) *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "models",
		Short: "List the System One models your provider offers",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ep, err := o.endpoint(e, true)
			if err != nil {
				return err
			}
			models, err := ep.ListModels(cmd.Context())
			if err != nil {
				return apiExit(err)
			}
			if asJSON {
				enc := json.NewEncoder(e.stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(models)
			}
			tw := tabwriter.NewWriter(e.stdout, 0, 4, 2, ' ', 0)
			fmt.Fprintln(tw, "NAME\tRELEASED\tDESCRIPTION")
			for _, m := range models {
				fmt.Fprintf(tw, "%s\t%s\t%s\n", m.Name, m.ReleaseDate, m.Description)
			}
			return tw.Flush()
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print JSON")
	addProviderFlags(cmd, o)
	return cmd
}

func newVersionCmd(e *env) *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the version",
		Args:  cobra.NoArgs,
		Run: func(*cobra.Command, []string) {
			fmt.Fprintf(e.stdout, "hunch %s (commit %s, built %s)\n", version(), commit, date)
		},
	}
}
