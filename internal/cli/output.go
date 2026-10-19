package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/yarolegovich/hunch/internal/typesafe"
)

// result is a record together with its answers, in spec order.
type result struct {
	rec     *record
	answers []answer
	err     error
}

// annotations encodes the answers as they appear under the output key.
func (r *result) annotations(s *spec) []byte {
	if s.single {
		b, _ := json.Marshal(r.answers[0])
		return b
	}
	var b bytes.Buffer
	b.WriteByte('{')
	for i, q := range s.questions {
		if i > 0 {
			b.WriteByte(',')
		}
		k, _ := json.Marshal(q.id)
		v, _ := json.Marshal(r.answers[i])
		b.Write(k)
		b.WriteByte(':')
		b.Write(v)
	}
	b.WriteByte('}')
	return b.Bytes()
}

// jsonLine is the original record plus the annotations under key. Objects
// keep their fields and order; anything else is wrapped.
func (r *result) jsonLine(s *spec, key string) []byte {
	ann := r.annotations(s)
	k, _ := json.Marshal(key)
	var b bytes.Buffer
	rec := r.rec
	switch {
	case rec.object:
		raw := bytes.TrimSpace(rec.raw)
		body := bytes.TrimSpace(raw[1 : len(raw)-1])
		b.WriteByte('{')
		if len(body) > 0 {
			b.Write(body)
			b.WriteByte(',')
		}
	case rec.mode == modeJSONL:
		b.WriteString(`{"value":`)
		b.Write(bytes.TrimSpace(rec.raw))
		b.WriteByte(',')
	case rec.mode == modeWhole:
		b.WriteByte('{')
		if rec.file != "" {
			f, _ := json.Marshal(rec.file)
			fmt.Fprintf(&b, `"file":%s,`, f)
		}
		t, _ := json.Marshal(string(rec.raw))
		fmt.Fprintf(&b, `"text":%s,`, t)
	default:
		l, _ := json.Marshal(string(rec.raw))
		fmt.Fprintf(&b, `{"line":%s,`, l)
	}
	b.Write(k)
	b.WriteByte(':')
	b.Write(ann)
	b.WriteString("}\n")
	return b.Bytes()
}

// rawLine is the original input. In whole mode with file arguments it is the
// file name.
func (r *result) rawLine() []byte {
	if r.rec.mode == modeWhole && r.rec.file != "" {
		return []byte(r.rec.file + "\n")
	}
	return append(bytes.TrimRight(r.rec.raw, "\n"), '\n')
}

// tsvLine is p<TAB>input, choice<TAB>conf<TAB>input or score<TAB>level<TAB>input.
func (r *result) tsvLine() []byte {
	a := r.answers[0]
	var cols []string
	switch a.kind {
	case typesafe.TypeNoul:
		cols = []string{fmtProb(a.p)}
	case typesafe.TypeChoice:
		cols = []string{a.choice, fmtProb(a.confidence)}
	case typesafe.TypeScore:
		cols = []string{strconv.FormatFloat(a.score, 'f', 2, 64), a.level}
	}
	in := strings.NewReplacer("\t", " ", "\n", " ").Replace(string(bytes.TrimRight(r.rawLine(), "\n")))
	return []byte(strings.Join(append(cols, in), "\t") + "\n")
}

func fmtProb(p float64) string { return strconv.FormatFloat(p, 'f', 2, 64) }

// printer formats kept records and writes them to stdout.
type printer struct {
	w      io.Writer
	spec   *spec
	format string // "json", "raw", "tsv" or "human"
	key    string
	color  bool
	width  int
}

func (p *printer) print(r *result) error {
	_, err := p.w.Write(p.line(r))
	return err
}

// line is one record in the chosen format.
func (p *printer) line(r *result) []byte {
	switch p.format {
	case "raw":
		return r.rawLine()
	case "tsv":
		return r.tsvLine()
	case "human":
		return []byte(p.human(r) + "\n")
	}
	return r.jsonLine(p.spec, p.key)
}

// fileLine is a record as written to a file: the chosen format, except that
// the terminal view becomes JSONL.
func (p *printer) fileLine(r *result) []byte {
	if p.format == "human" {
		return r.jsonLine(p.spec, p.key)
	}
	return p.line(r)
}

const (
	ansiReset = "\x1b[0m"
	ansiBold  = "\x1b[1m"
	ansiDim   = "\x1b[2m"
	ansiRed   = "\x1b[31m"
	ansiGreen = "\x1b[32m"
	ansiYel   = "\x1b[33m"
	ansiCyan  = "\x1b[36m"
)

func (p *printer) paint(code, s string) string {
	if !p.color {
		return s
	}
	return code + s + ansiReset
}

// Thresholds for wording a yes/no probability in the terminal view. Only the
// wording uses them; --min and --max compare the probability itself.
const (
	yesFrom = 0.7
	noUpTo  = 0.3
)

// verdict words a yes/no probability as yes, maybe or no.
func verdict(p float64) (word, color string) {
	switch {
	case p >= yesFrom:
		return "yes", ansiBold + ansiGreen
	case p <= noUpTo:
		return "no", ansiDim
	}
	return "maybe", ansiYel
}

// heat colours a probability: high is green, middling yellow, low dim.
func (p *printer) heat(v float64, s string) string {
	switch {
	case v >= 0.8:
		return p.paint(ansiBold+ansiGreen, s)
	case v >= 0.5:
		return p.paint(ansiYel, s)
	case v >= 0.2:
		return p.paint(ansiRed, s)
	default:
		return p.paint(ansiDim, s)
	}
}

// human renders one compact line: the answers, then the input, truncated to
// the terminal width.
func (p *printer) human(r *result) string {
	var parts []string
	plain := 0
	add := func(colored, raw string) {
		parts = append(parts, colored)
		plain += utf8.RuneCountInString(raw) + 1
	}
	for i, a := range r.answers {
		prefix := ""
		if !p.spec.single {
			prefix = p.spec.questions[i].id + " "
			add(p.paint(ansiDim, strings.TrimSpace(prefix)), prefix[:len(prefix)-1])
		}
		switch a.kind {
		case typesafe.TypeNoul:
			word, color := verdict(a.p)
			add(p.paint(color, fmt.Sprintf("%-5s", word))+p.paint(ansiDim, " ("+fmtProb(a.p)+")"),
				fmt.Sprintf("%-5s (%s)", word, fmtProb(a.p)))
		case typesafe.TypeChoice:
			s := fmtProb(a.value())
			add(p.heat(a.value(), s), s)
			name := pad(a.choice, p.spec.questions[i].labels)
			add(p.paint(ansiCyan, name), name)
		case typesafe.TypeScore:
			s := strconv.FormatFloat(a.score, 'f', 2, 64)
			add(p.paint(ansiBold, s), s)
			name := pad(a.level, p.spec.questions[i].labels)
			add(p.paint(ansiCyan, name), name)
		}
	}
	text := p.display(r.rec)
	if p.width > 0 {
		if room := p.width - plain - 2; room > 0 && utf8.RuneCountInString(text) > room {
			text = string([]rune(text)[:max(room-1, 0)]) + "…"
		}
	}
	return strings.Join(parts, " ") + "  " + text
}

// pad right-pads name to the longest label so columns line up.
func pad(name string, labels []label) string {
	w := 0
	for _, l := range labels {
		w = max(w, utf8.RuneCountInString(l.name))
	}
	return name + strings.Repeat(" ", max(w-utf8.RuneCountInString(name), 0))
}

// display is the input as shown in the terminal view.
func (p *printer) display(rec *record) string {
	switch {
	case rec.mode == modeWhole && rec.file != "":
		return rec.file
	case rec.mode == modeWhole:
		first, _, _ := strings.Cut(strings.TrimSpace(string(rec.raw)), "\n")
		return first
	case rec.mode == modeJSONL:
		if s, ok := rec.state.(string); ok {
			return strings.ReplaceAll(s, "\n", " ")
		}
		var b bytes.Buffer
		if json.Compact(&b, rec.raw) == nil {
			return b.String()
		}
	}
	return string(rec.raw)
}
