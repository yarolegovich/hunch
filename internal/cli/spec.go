package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/expr-lang/expr"
	"github.com/expr-lang/expr/vm"
	"gopkg.in/yaml.v3"

	"github.com/yarolegovich/hunch/internal/typesafe"
)

// label is a Choice option or Score level with an optional description.
type label struct {
	name string
	desc string
}

// question is one question as hunch knows it, before it becomes an API
// question.
type question struct {
	id      string
	kind    typesafe.QuestionType
	text    string
	labels  []label // choice options or score levels
	yes, no string  // noul criteria
}

// spec is everything asked of each record.
type spec struct {
	questions []question
	// single is true for yes/pick/score: the answer is written directly under
	// the output key instead of under its question id.
	single bool
	// context is guidance sent inside each question's instructions.
	context string
	keep    *vm.Program
}

func (s *spec) apiQuestions() typesafe.Questions {
	qs := make(typesafe.Questions, len(s.questions))
	for _, q := range s.questions {
		var instr any = q.text
		if s.context != "" {
			instr = map[string]string{"context": s.context, "question": q.text}
		}
		switch q.kind {
		case typesafe.TypeNoul:
			aq := typesafe.Noul(instr)
			if q.yes != "" || q.no != "" {
				aq = aq.WithCriteria(nilIfEmpty(q.yes), nilIfEmpty(q.no))
			}
			qs[q.id] = aq
		case typesafe.TypeChoice:
			opts := make([]typesafe.ChoiceOption, len(q.labels))
			for i, l := range q.labels {
				opts[i] = typesafe.Opt(l.name, nilIfEmpty(l.desc))
			}
			qs[q.id] = typesafe.ChoiceOf(instr, opts...)
		case typesafe.TypeScore:
			levels := make([]any, len(q.labels))
			for i, l := range q.labels {
				levels[i] = l.name
				if l.desc != "" {
					levels[i] = map[string]string{"level": l.name, "description": l.desc}
				}
			}
			qs[q.id] = typesafe.Score(instr, levels...)
		}
	}
	return qs
}

// placeholder is the -o template variable this spec can fill: {choice} for
// pick, {level} for score, none otherwise.
func (s *spec) placeholder() string {
	if !s.single {
		return ""
	}
	switch s.questions[0].kind {
	case typesafe.TypeChoice:
		return "{choice}"
	case typesafe.TypeScore:
		return "{level}"
	}
	return ""
}

func (s *spec) validate() error {
	return typesafe.Request{Questions: s.apiQuestions()}.Validate()
}

func nilIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// answer is one answer as hunch prints it.
type answer struct {
	kind       typesafe.QuestionType
	p          float64 // noul
	choice     string  // choice
	score      float64 // score
	level      string  // score: the level nearest to score
	probs      []prob  // choice and score, in option or level order
	confidence float64
}

type prob struct {
	name string
	p    float64
}

func newAnswer(q question, a typesafe.Answer) answer {
	out := answer{kind: q.kind, confidence: a.Confidence}
	switch q.kind {
	case typesafe.TypeNoul:
		out.p = a.Noul
	case typesafe.TypeChoice:
		out.choice = a.Choice
		for _, l := range q.labels {
			out.probs = append(out.probs, prob{l.name, a.Probabilities[l.name]})
		}
	case typesafe.TypeScore:
		out.score = a.Score
		lvl := min(max(a.Level(), 0), len(q.labels)-1)
		out.level = q.labels[lvl].name
		for i, l := range q.labels {
			out.probs = append(out.probs, prob{l.name, a.LevelProbability(i)})
		}
	}
	return out
}

// value is what --min, --max and --sort compare: the yes probability, the
// chosen option's probability, or the score.
func (a answer) value() float64 {
	switch a.kind {
	case typesafe.TypeNoul:
		return a.p
	case typesafe.TypeChoice:
		for _, p := range a.probs {
			if p.name == a.choice {
				return p.p
			}
		}
		return 0
	default:
		return a.score
	}
}

// label is the chosen option or the nearest level; empty for yes/no.
func (a answer) label() string {
	if a.kind == typesafe.TypeScore {
		return a.level
	}
	return a.choice
}

func (a answer) hasConfidence() bool { return a.kind != typesafe.TypeNoul }

func (a answer) MarshalJSON() ([]byte, error) {
	var b bytes.Buffer
	switch a.kind {
	case typesafe.TypeNoul:
		fmt.Fprintf(&b, `{"p":%s}`, num(a.p))
		return b.Bytes(), nil
	case typesafe.TypeChoice:
		c, _ := json.Marshal(a.choice)
		fmt.Fprintf(&b, `{"choice":%s,"probs":`, c)
	case typesafe.TypeScore:
		l, _ := json.Marshal(a.level)
		fmt.Fprintf(&b, `{"score":%s,"level":%s,"probs":`, num(a.score), l)
	}
	b.WriteByte('{')
	for i, p := range a.probs {
		if i > 0 {
			b.WriteByte(',')
		}
		k, _ := json.Marshal(p.name)
		fmt.Fprintf(&b, "%s:%s", k, num(p.p))
	}
	fmt.Fprintf(&b, `},"confidence":%s}`, num(a.confidence))
	return b.Bytes(), nil
}

// num formats a probability compactly; non-finite values become 0 so the
// output stays valid JSON.
func num(f float64) string {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return "0"
	}
	return strconv.FormatFloat(f, 'f', -1, 64)
}

// exprEnv exposes an answer to keep expressions.
func (a answer) exprEnv() map[string]any {
	m := map[string]any{"confidence": a.confidence}
	switch a.kind {
	case typesafe.TypeNoul:
		m["p"] = a.p
	case typesafe.TypeChoice:
		m["choice"] = a.choice
	case typesafe.TypeScore:
		m["score"] = a.score
		m["level"] = a.level
	}
	probs := make(map[string]any, len(a.probs))
	for _, p := range a.probs {
		probs[p.name] = p.p
	}
	m["probs"] = probs
	return m
}

func compileKeep(src string) (*vm.Program, error) {
	if src == "" {
		return nil, nil
	}
	p, err := expr.Compile(src, expr.AsBool())
	if err != nil {
		return nil, fmt.Errorf("keep expression: %w", err)
	}
	return p, nil
}

// questionSet is the shape of an ask file and of a preset in the config file.
//
//	context: optional guidance
//	questions:
//	  auth:  { type: noul,   q: "Is this an authentication failure?" }
//	  sev:   { type: choice, q: "Severity?", options: [low, medium, high] }
//	  noise: { type: score,  q: "How noisy is this line?", levels: [signal, mixed, noise] }
//	keep: "auth.p > 0.8 && sev.choice != 'low'"
type questionSet struct {
	Context   string    `yaml:"context"`
	Questions yaml.Node `yaml:"questions"`
	Keep      string    `yaml:"keep"`
}

type questionDef struct {
	Type    string      `yaml:"type"`
	Q       string      `yaml:"q"`
	YesWhen string      `yaml:"yes_when"`
	NoWhen  string      `yaml:"no_when"`
	Options []yaml.Node `yaml:"options"`
	Levels  []yaml.Node `yaml:"levels"`
}

// spec builds the spec; flags override the set's context and keep.
func (qs *questionSet) spec(o *options) (*spec, error) {
	n := &qs.Questions
	if n.Kind != yaml.MappingNode || len(n.Content) == 0 {
		return nil, fmt.Errorf("questions must be a non-empty mapping of id to question")
	}
	s := &spec{context: qs.Context}
	if o.context != "" {
		s.context = o.context
	}
	for i := 0; i < len(n.Content); i += 2 {
		id := n.Content[i].Value
		var d questionDef
		if err := n.Content[i+1].Decode(&d); err != nil {
			return nil, fmt.Errorf("question %q: %w", id, err)
		}
		q := question{id: id, kind: typesafe.QuestionType(d.Type), text: d.Q, yes: d.YesWhen, no: d.NoWhen}
		if q.text == "" {
			return nil, fmt.Errorf("question %q: missing q", id)
		}
		var raw []yaml.Node
		switch q.kind {
		case typesafe.TypeNoul:
		case typesafe.TypeChoice:
			raw = d.Options
		case typesafe.TypeScore:
			raw = d.Levels
		default:
			return nil, fmt.Errorf("question %q: type must be noul, choice or score", id)
		}
		var err error
		if q.labels, err = yamlLabels(raw); err != nil {
			return nil, fmt.Errorf("question %q: %w", id, err)
		}
		s.questions = append(s.questions, q)
	}
	keep := qs.Keep
	if o.keep != "" {
		keep = o.keep
	}
	var err error
	if s.keep, err = compileKeep(keep); err != nil {
		return nil, err
	}
	return s, s.validate()
}

// yamlLabels accepts "name", "name=description" or {name: description} items.
// Unlike flag values, items are never split on commas.
func yamlLabels(nodes []yaml.Node) ([]label, error) {
	var out []label
	seen := map[string]bool{}
	for _, n := range nodes {
		var l label
		switch {
		case n.Kind == yaml.ScalarNode:
			name, desc, _ := strings.Cut(n.Value, "=")
			l = label{strings.TrimSpace(name), strings.TrimSpace(desc)}
		case n.Kind == yaml.MappingNode && len(n.Content) == 2:
			l = label{n.Content[0].Value, n.Content[1].Value}
		default:
			return nil, fmt.Errorf("line %d: an option is a string or a single name: description pair", n.Line)
		}
		if l.name == "" || seen[l.name] {
			return nil, fmt.Errorf("line %d: empty or duplicate name %q", n.Line, l.name)
		}
		seen[l.name] = true
		out = append(out, l)
	}
	return out, nil
}
