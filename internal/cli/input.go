package cli

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

type inputMode string

const (
	modeLines inputMode = "lines"
	modeJSONL inputMode = "jsonl"
	modeWhole inputMode = "whole"
)

// record is one unit of input.
type record struct {
	seq  int
	mode inputMode
	// raw is the original line, JSON value or file contents.
	raw []byte
	// state is what is sent to the API.
	state any
	// object is set for jsonl records whose value is a JSON object.
	object bool
	// file is the source file name in whole mode, empty for stdin.
	file string
	// err is a per-record input error, such as nothing at --jsonpath.
	err error
}

// source is one input: a named file or stdin.
type source struct {
	name string // "" for stdin
	r    io.Reader
}

func openSources(args []string, stdin io.Reader) ([]source, func(), error) {
	if len(args) == 0 {
		return []source{{r: stdin}}, func() {}, nil
	}
	var files []*os.File
	closeAll := func() {
		for _, f := range files {
			f.Close()
		}
	}
	var out []source
	for _, a := range args {
		if a == "-" {
			out = append(out, source{r: stdin})
			continue
		}
		f, err := os.Open(a)
		if err != nil {
			closeAll()
			return nil, nil, &exitError{exitUsage, err}
		}
		files = append(files, f)
		out = append(out, source{name: a, r: f})
	}
	return out, closeAll, nil
}

// reader turns sources into records.
type reader struct {
	mode inputMode // modeLines, modeJSONL, modeWhole or "auto"
	path *jsonPath
	key  string
	srcs []source
	seq  int
}

func newReader(srcs []source, o *options) (*reader, error) {
	r := &reader{mode: inputMode(o.in), srcs: srcs, key: o.key}
	if o.jsonpath != "" {
		var err error
		if r.path, err = parseJSONPath(o.jsonpath); err != nil {
			return nil, usageErrorf("--jsonpath: %v", err)
		}
	}
	return r, nil
}

// jsonPath is a JSONPath that selects a single value: $.a.b, $.items[0],
// $['key with spaces']. The leading $ is optional, so jq-style .a.b works too.
type jsonPath struct {
	src  string
	segs []pathSeg
}

type pathSeg struct {
	key   string
	index int // used when isIdx
	isIdx bool
}

func parseJSONPath(src string) (*jsonPath, error) {
	s := strings.TrimSpace(src)
	s = strings.TrimPrefix(s, "$")
	p := &jsonPath{src: src}
	for len(s) > 0 {
		switch {
		case strings.HasPrefix(s, ".."):
			return nil, fmt.Errorf("%q: recursive descent (..) can match many values; name one", src)
		case s[0] == '.':
			s = s[1:]
			n := strings.IndexAny(s, ".[")
			if n < 0 {
				n = len(s)
			}
			name := s[:n]
			if name == "" {
				return nil, fmt.Errorf("%q: empty name", src)
			}
			if name == "*" {
				return nil, fmt.Errorf("%q: wildcards can match many values; name one", src)
			}
			if strings.ContainsAny(name, " \t'\"]*") {
				return nil, fmt.Errorf("%q: write names with spaces or symbols as ['%s']", src, name)
			}
			p.segs = append(p.segs, pathSeg{key: name})
			s = s[n:]
		case s[0] == '[':
			end := strings.IndexByte(s, ']')
			if end < 0 {
				return nil, fmt.Errorf("%q: missing ]", src)
			}
			in := strings.TrimSpace(s[1:end])
			switch {
			case in == "*" || strings.HasPrefix(in, "?") || strings.ContainsAny(in, ":,"):
				return nil, fmt.Errorf("%q: [%s] can match many values; name one", src, in)
			case len(in) >= 2 && (in[0] == '\'' || in[0] == '"') && in[len(in)-1] == in[0]:
				p.segs = append(p.segs, pathSeg{key: in[1 : len(in)-1]})
			default:
				n, err := strconv.Atoi(in)
				if err != nil || n < 0 {
					return nil, fmt.Errorf("%q: [%s] must be an array index or a quoted name", src, in)
				}
				p.segs = append(p.segs, pathSeg{index: n, isIdx: true})
			}
			s = s[end+1:]
		default:
			if len(p.segs) > 0 {
				return nil, fmt.Errorf("%q: expected . or [ before %q", src, s)
			}
			s = "." + s // a bare first name: body.text
		}
	}
	if len(p.segs) == 0 {
		return nil, fmt.Errorf("%q selects the whole record; leave --jsonpath out for that", src)
	}
	return p, nil
}

// get returns the value the path selects.
func (p *jsonPath) get(v any) (any, error) {
	for _, seg := range p.segs {
		switch x := v.(type) {
		case map[string]any:
			if seg.isIdx {
				return nil, fmt.Errorf("nothing at %s: not an array", p.src)
			}
			var ok bool
			if v, ok = x[seg.key]; !ok {
				return nil, fmt.Errorf("nothing at %s", p.src)
			}
		case []any:
			if !seg.isIdx || seg.index >= len(x) {
				return nil, fmt.Errorf("nothing at %s", p.src)
			}
			v = x[seg.index]
		default:
			return nil, fmt.Errorf("nothing at %s", p.src)
		}
	}
	if v == nil {
		return nil, fmt.Errorf("%s is null", p.src)
	}
	return v, nil
}

// each calls fn for every record, stopping early if fn returns false.
func (r *reader) each(fn func(*record) bool) error {
	for _, src := range r.srcs {
		if r.mode == modeWhole {
			data, err := io.ReadAll(src.r)
			if err != nil {
				return err
			}
			if !fn(r.next(modeWhole, data, src.name)) {
				return nil
			}
			continue
		}
		br := bufio.NewReaderSize(src.r, 64<<10)
		for {
			line, err := br.ReadBytes('\n')
			if len(line) > 0 {
				line = bytes.TrimRight(line, "\r\n")
				if len(bytes.TrimSpace(line)) > 0 {
					if r.mode == "auto" {
						r.mode = detectMode(line)
						if r.mode != modeJSONL && r.path != nil {
							return usageErrorf("--jsonpath needs JSONL input, but the first line is not JSON")
						}
					}
					if !fn(r.next(r.mode, line, src.name)) {
						return nil
					}
				}
			}
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				return err
			}
		}
	}
	return nil
}

func detectMode(line []byte) inputMode {
	t := bytes.TrimSpace(line)
	if (t[0] == '{' || t[0] == '[') && json.Valid(t) {
		return modeJSONL
	}
	return modeLines
}

func (r *reader) next(mode inputMode, raw []byte, file string) *record {
	r.seq++
	rec := &record{seq: r.seq, mode: mode, raw: raw}
	switch mode {
	case modeLines:
		rec.state = string(raw)
	case modeWhole:
		rec.state = string(raw)
		rec.file = file
	case modeJSONL:
		var v any
		d := json.NewDecoder(bytes.NewReader(raw))
		d.UseNumber()
		if err := d.Decode(&v); err != nil {
			rec.err = fmt.Errorf("line is not JSON: %w", err)
			return rec
		}
		if obj, ok := v.(map[string]any); ok {
			rec.object = true
			if _, clash := obj[r.key]; clash {
				rec.err = fmt.Errorf("record already has a %q field; pick another with --key", r.key)
				return rec
			}
		}
		rec.state = v
		if r.path != nil {
			if rec.state, rec.err = r.path.get(v); rec.err != nil {
				return rec
			}
		}
	}
	return rec
}
