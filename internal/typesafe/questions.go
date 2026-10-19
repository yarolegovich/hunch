package typesafe

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// QuestionType names a System One question type.
type QuestionType string

const (
	TypeNoul   QuestionType = "noul"
	TypeChoice QuestionType = "choice"
	TypeScore  QuestionType = "score"
)

// MaxChoiceOptions and MaxScoreLevels are the limits the API accepts.
const (
	MaxChoiceOptions = 255
	MaxScoreLevels   = 10
)

// Questions maps a caller-chosen id to a question. Answers come back under the
// same ids.
type Questions map[string]Question

// Question is one typed question. Instructions and criteria may be a string or
// any JSON-encodable structure; build questions with Noul, Choice and Score.
type Question struct {
	Type         QuestionType
	Instructions any
	criteria     any
}

// Noul asks a yes/no question; the answer is the probability of yes.
func Noul(instructions any) Question {
	return Question{Type: TypeNoul, Instructions: instructions}
}

// WithCriteria describes what a yes and a no mean for a Noul question. Either
// side may be nil.
func (q Question) WithCriteria(yes, no any) Question {
	if q.Type == TypeNoul {
		q.criteria = noulCriteria{True: yes, False: no}
	}
	return q
}

// ChoiceOption is one Choice option with an optional description.
type ChoiceOption struct {
	Name        string
	Description any
}

// Opt builds a ChoiceOption with a description.
func Opt(name string, description any) ChoiceOption {
	return ChoiceOption{Name: name, Description: description}
}

// Choice asks the model to pick one of the named options.
func Choice(instructions any, options ...string) Question {
	opts := make([]ChoiceOption, len(options))
	for i, o := range options {
		opts[i] = ChoiceOption{Name: o}
	}
	return ChoiceOf(instructions, opts...)
}

// ChoiceOf is Choice with option descriptions. Option order is preserved on
// the wire.
func ChoiceOf(instructions any, options ...ChoiceOption) Question {
	return Question{Type: TypeChoice, Instructions: instructions, criteria: choiceCriteria(options)}
}

// Score rates the state along ordered levels, lowest first. Each level is a
// string or a JSON-encodable description.
func Score(instructions any, levels ...any) Question {
	return Question{Type: TypeScore, Instructions: instructions, criteria: levels}
}

// Validate reports obvious mistakes before a request is sent.
func (q Question) Validate() error {
	switch q.Type {
	case TypeNoul:
	case TypeChoice:
		opts, _ := q.criteria.(choiceCriteria)
		if len(opts) < 2 {
			return fmt.Errorf("choice needs at least 2 options")
		}
		if len(opts) > MaxChoiceOptions {
			return fmt.Errorf("choice has %d options, at most %d allowed", len(opts), MaxChoiceOptions)
		}
		seen := make(map[string]bool, len(opts))
		for _, o := range opts {
			if seen[o.Name] {
				return fmt.Errorf("duplicate choice option %q", o.Name)
			}
			seen[o.Name] = true
		}
	case TypeScore:
		levels, _ := q.criteria.([]any)
		if len(levels) < 2 {
			return fmt.Errorf("score needs at least 2 levels")
		}
		if len(levels) > MaxScoreLevels {
			return fmt.Errorf("score has %d levels, at most %d allowed", len(levels), MaxScoreLevels)
		}
	default:
		return fmt.Errorf("unknown question type %q", q.Type)
	}
	return nil
}

func (q Question) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Type         QuestionType `json:"type"`
		Instructions any          `json:"instructions"`
		Criteria     any          `json:"criteria,omitempty"`
	}{q.Type, q.Instructions, q.criteria})
}

type noulCriteria struct {
	True  any `json:"true,omitempty"`
	False any `json:"false,omitempty"`
}

// choiceCriteria encodes as a JSON object in option order.
type choiceCriteria []ChoiceOption

func (c choiceCriteria) MarshalJSON() ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteByte('{')
	for i, o := range c {
		if i > 0 {
			buf.WriteByte(',')
		}
		k, err := json.Marshal(o.Name)
		if err != nil {
			return nil, err
		}
		v, err := json.Marshal(o.Description)
		if err != nil {
			return nil, err
		}
		buf.Write(k)
		buf.WriteByte(':')
		buf.Write(v)
	}
	buf.WriteByte('}')
	return buf.Bytes(), nil
}
