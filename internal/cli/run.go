package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/expr-lang/expr"
	"golang.org/x/term"

	"github.com/yarolegovich/hunch/internal/provider"
	"github.com/yarolegovich/hunch/internal/typesafe"
)

// executor runs a spec over the input and decides what to print.
type executor struct {
	e      *env
	o      *options
	spec   *spec
	qs     typesafe.Questions
	ep     *provider.Endpoint
	client *typesafe.Client
	out    *printer
	dest   *fileSet // -o; nil writes to stdout
	unsure *fileSet

	calls, inTokens, outTokens atomic.Int64
	records, kept, failed      int
	sorted                     []*result
}

// inputError marks a problem with a record itself rather than with the API.
type inputError struct{ err error }

func (e *inputError) Error() string { return e.err.Error() }
func (e *inputError) Unwrap() error { return e.err }

func execute(ctx context.Context, e *env, o *options, s *spec, files []string) error {
	srcs, closeSrcs, err := openSources(files, e.stdin)
	if err != nil {
		return err
	}
	defer closeSrcs()
	rd, err := newReader(srcs, o)
	if err != nil {
		return err
	}
	if o.dryRun {
		return dryRun(e, o, s, rd)
	}

	ep, err := o.endpoint(e, true)
	if err != nil {
		return err
	}
	x := &executor{e: e, o: o, spec: s, qs: s.apiQuestions(), ep: ep}
	x.client = ep.Client(
		typesafe.WithMaxRetries(o.retries),
		typesafe.WithUserAgent("hunch/"+version()),
		typesafe.WithRetryHook(func(int, error, time.Duration) { x.calls.Add(1) }),
	)
	x.out = &printer{w: e.stdout, spec: s, key: o.key, format: outputFormat(e, o)}
	if x.out.format == "human" {
		x.out.color = e.color(e.stdout)
		if f, ok := e.stdout.(*os.File); ok {
			x.out.width, _, _ = term.GetSize(int(f.Fd()))
		}
	}
	placeholder := s.placeholder()
	if o.output != "" {
		if err := checkTemplate(o.output, placeholder); err != nil {
			return err
		}
		x.dest = &fileSet{tmpl: o.output, placeholder: placeholder}
		if placeholder == "" || !strings.Contains(o.output, placeholder) {
			// Like a shell redirect, a plain -o file exists even if nothing is kept.
			if err := x.dest.write("", nil); err != nil {
				return err
			}
		}
	}
	switch {
	case o.unsure != "":
		x.unsure = &fileSet{tmpl: o.unsure}
	case o.conf > 0 && placeholder != "" && strings.Contains(o.output, placeholder):
		x.unsure = &fileSet{tmpl: strings.ReplaceAll(o.output, placeholder, "_unsure")}
	}
	defer x.dest.close()
	defer x.unsure.close()

	start := time.Now()
	err = x.pipeline(ctx, rd)
	if err == nil && o.sort {
		err = x.flushSorted()
	}
	if o.stats {
		fmt.Fprintf(e.stderr, "hunch: %d records, %d kept, %d failed, %d calls, %d input tokens, %d output tokens, %s\n",
			x.records, x.kept, x.failed, x.calls.Load(), x.inTokens.Load(), x.outTokens.Load(),
			time.Since(start).Round(time.Millisecond))
	}
	if err != nil {
		return err
	}
	switch {
	case x.failed > 0:
		return silentExit(exitPartial)
	case x.kept == 0:
		return silentExit(exitNoneKept)
	}
	return nil
}

func outputFormat(e *env, o *options) string {
	switch {
	case o.raw:
		return "raw"
	case o.tsv:
		return "tsv"
	case o.jsonOut || o.output != "" || !e.isTerminal(e.stdout):
		return "json"
	}
	return "human"
}

func dryRun(e *env, o *options, s *spec, rd *reader) error {
	var first *record
	if err := rd.each(func(r *record) bool { first = r; return false }); err != nil {
		return err
	}
	if first == nil {
		return usageErrorf("no input records")
	}
	if first.err != nil {
		return usageErrorf("record 1: %v", first.err)
	}
	ep, err := o.endpoint(e, false)
	if err != nil {
		return err
	}
	body, err := ep.Client().Body(typesafe.Request{State: first.state, Questions: s.apiQuestions()})
	if err != nil {
		return err
	}
	var out bytes.Buffer
	if err := json.Indent(&out, body, "", "  "); err != nil {
		return err
	}
	fmt.Fprintln(e.stdout, out.String())
	return nil
}

type job struct {
	res  *result
	done chan struct{}
}

// pipeline reads records, asks about them with o.jobs workers, and handles
// results in input order (or completion order with --unordered).
func (x *executor) pipeline(parent context.Context, rd *reader) error {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	ordered := !x.o.unordered

	jobs := make(chan *job)
	order := make(chan *job, x.o.jobs*4)
	finished := make(chan *job, x.o.jobs*4)

	var readErr error
	go func() {
		defer close(order)
		defer close(jobs)
		readErr = rd.each(func(rec *record) bool {
			j := &job{res: &result{rec: rec}, done: make(chan struct{})}
			if ordered {
				select {
				case order <- j:
				case <-ctx.Done():
					return false
				}
			}
			select {
			case jobs <- j:
				return true
			case <-ctx.Done():
				j.res.err = ctx.Err()
				close(j.done)
				return false
			}
		})
	}()

	var wg sync.WaitGroup
	for range x.o.jobs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range jobs {
				x.ask(ctx, j.res)
				close(j.done)
				if !ordered {
					finished <- j
				}
			}
		}()
	}
	go func() { wg.Wait(); close(finished) }()

	var fatal error
	handle := func(r *result) {
		if fatal != nil {
			return
		}
		if fatal = x.handle(r); fatal != nil {
			cancel()
		}
	}
	if ordered {
		for j := range order {
			<-j.done
			handle(j.res)
		}
	} else {
		for j := range finished {
			handle(j.res)
		}
	}
	if fatal == nil && parent.Err() != nil {
		fatal = &exitError{130, parent.Err()}
	}
	if fatal != nil {
		return fatal
	}
	if readErr != nil {
		var ee *exitError
		if errors.As(readErr, &ee) {
			return readErr
		}
		return &exitError{exitUsage, fmt.Errorf("reading input: %w", readErr)}
	}
	return nil
}

func (x *executor) ask(ctx context.Context, r *result) {
	if r.rec.err != nil {
		r.err = &inputError{r.rec.err}
		return
	}
	x.calls.Add(1)
	res, err := x.client.Ask(ctx, typesafe.Request{
		State:       r.rec.state,
		Questions:   x.qs,
		LongContext: x.o.longContext,
	})
	if err != nil {
		r.err = err
		return
	}
	x.inTokens.Add(int64(res.Usage.InputTokens))
	x.outTokens.Add(int64(res.Usage.OutputTokens))
	for _, q := range x.spec.questions {
		a, ok := res.Answers[q.id]
		if !ok {
			r.err = fmt.Errorf("response has no answer for %q", q.id)
			return
		}
		r.answers = append(r.answers, newAnswer(q, a))
	}
}

// handle decides what happens to one finished record. A non-nil error stops
// the run.
func (x *executor) handle(r *result) error {
	if r.err != nil {
		if errors.Is(r.err, context.Canceled) {
			return nil
		}
		return x.fail(r)
	}
	x.records++
	keep, unsure, err := x.classify(r)
	if err != nil {
		r.err = &inputError{err}
		return x.fail(r)
	}
	if unsure {
		if x.unsure != nil {
			return x.unsure.write("", x.out.fileLine(r))
		}
		return nil
	}
	if !keep {
		return nil
	}
	x.kept++
	if x.o.sort {
		x.sorted = append(x.sorted, r)
		return nil
	}
	return x.emit(r)
}

// emit writes a kept record to -o or stdout.
func (x *executor) emit(r *result) error {
	if x.dest == nil {
		return x.out.print(r)
	}
	name := ""
	if x.spec.single {
		name = r.answers[0].label()
	}
	return x.dest.write(name, x.out.fileLine(r))
}

func (x *executor) fail(r *result) error {
	err := fmt.Errorf("record %d: %w", r.rec.seq, r.err)
	var inErr *inputError
	switch {
	case typesafe.IsAuthError(r.err):
		return &exitError{exitAuth, fmt.Errorf("%w (check %s or run 'hunch auth login')", r.err, x.ep.KeyEnv)}
	case x.o.keepGoing:
		x.records++
		x.failed++
		fmt.Fprintf(x.e.stderr, "hunch: %v\n", err)
		return nil
	case errors.As(r.err, &inErr):
		return &exitError{exitUsage, err}
	}
	return &exitError{exitAPI, err}
}

// classify applies --conf, --min/--max, --only and the keep expression.
func (x *executor) classify(r *result) (keep, unsure bool, err error) {
	if x.o.conf > 0 {
		for _, a := range r.answers {
			if a.hasConfidence() && a.confidence < x.o.conf {
				return false, true, nil
			}
		}
	}
	if x.spec.single {
		a := r.answers[0]
		v := a.value()
		if x.o.hasMin && v < x.o.min || x.o.hasMax && v > x.o.max {
			return false, false, nil
		}
		if len(x.o.only) > 0 && !slices.Contains(x.o.only, a.choice) {
			return false, false, nil
		}
	}
	if x.spec.keep != nil {
		env := make(map[string]any, len(r.answers))
		for i, q := range x.spec.questions {
			env[q.id] = r.answers[i].exprEnv()
		}
		out, err := expr.Run(x.spec.keep, env)
		if err != nil {
			return false, false, fmt.Errorf("keep expression: %w", err)
		}
		keep, _ = out.(bool)
		return keep, false, nil
	}
	return true, false, nil
}

func (x *executor) flushSorted() error {
	sort.SliceStable(x.sorted, func(i, j int) bool {
		return x.sorted[i].answers[0].value() > x.sorted[j].answers[0].value()
	})
	if n := x.o.top; n > 0 && len(x.sorted) > n {
		x.sorted = x.sorted[:n]
		x.kept = n
	}
	for _, r := range x.sorted {
		if err := x.emit(r); err != nil {
			return err
		}
	}
	return nil
}

// checkTemplate rejects {placeholders} the command can't fill.
func checkTemplate(tmpl, placeholder string) error {
	rest := tmpl
	if placeholder != "" {
		rest = strings.ReplaceAll(rest, placeholder, "")
	}
	if i := strings.IndexByte(rest, '{'); i >= 0 && strings.IndexByte(rest[i:], '}') > 0 {
		if placeholder == "" {
			return usageErrorf("-o: only pick ({choice}) and score ({level}) can write a file per answer")
		}
		return usageErrorf("-o: this command fills %s; got %q", placeholder, tmpl)
	}
	return nil
}

// fileSet writes records to the file tmpl names. If tmpl contains the
// placeholder, each answer gets its own file. Files are truncated the first
// time they are written in a run.
type fileSet struct {
	tmpl        string
	placeholder string
	files       map[string]*os.File
}

var unsafeName = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

func (s *fileSet) write(name string, line []byte) error {
	path := s.tmpl
	if s.placeholder != "" && strings.Contains(path, s.placeholder) {
		name = unsafeName.ReplaceAllString(name, "_")
		if name == "" || name == "." || name == ".." {
			name = "_"
		}
		path = strings.ReplaceAll(path, s.placeholder, name)
	}
	f, ok := s.files[path]
	if !ok {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return &exitError{exitUsage, err}
		}
		var err error
		if f, err = os.Create(path); err != nil {
			return &exitError{exitUsage, err}
		}
		if s.files == nil {
			s.files = map[string]*os.File{}
		}
		s.files[path] = f
	}
	_, err := f.Write(line)
	return err
}

func (s *fileSet) close() {
	if s == nil {
		return
	}
	for _, f := range s.files {
		f.Close()
	}
}
