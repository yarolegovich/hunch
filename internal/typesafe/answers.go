package typesafe

import (
	"math"
	"strconv"
)

// Response is the result of a System One call.
type Response struct {
	// Model is the versioned model id that answered, e.g. "jev-1.13.0".
	Model   string            `json:"model"`
	Answers map[string]Answer `json:"answers"`
	Usage   Usage             `json:"usage"`
}

// Usage is the token usage of one call.
type Usage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

// Answer is one typed answer. Which fields are set depends on Type.
type Answer struct {
	Type QuestionType `json:"type"`

	// Noul is the probability of yes, from 0 to 1.
	Noul float64 `json:"noul,omitempty"`

	// Choice is the highest-probability option.
	Choice string `json:"choice,omitempty"`

	// Score is the probability-weighted level index; it can land between levels.
	Score float64 `json:"score,omitempty"`

	// Legend maps a level index ("0", "1", ...) back to its description.
	Legend map[string]any `json:"legend,omitempty"`

	// Probabilities maps each option (Choice) or level index (Score) to its
	// probability.
	Probabilities map[string]float64 `json:"probabilities,omitempty"`

	// Confidence is how certain the model is, from 0 to 1 (Choice and Score).
	Confidence float64 `json:"confidence,omitempty"`
}

// Level returns the index of the level nearest to Score.
func (a Answer) Level() int {
	return int(math.Round(a.Score))
}

// LevelProbability returns the probability of the level at index i.
func (a Answer) LevelProbability(i int) float64 {
	return a.Probabilities[strconv.Itoa(i)]
}
