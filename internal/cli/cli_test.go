package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zalando/go-keyring"
)

// fakeAPI answers deterministically from the state text:
//   - noul: 0.95 if the state mentions "401" or "auth", else 0.05
//   - choice: the first option named in the state, else the last option;
//     confidence 0.3 if the state contains "?", else 0.9
//   - score: the index of the first level named in the state, else 0
type fakeAPI struct {
	status atomic.Int32 // non-zero forces this status
	calls  atomic.Int32
	jitter bool

	mu      sync.Mutex
	last    *http.Request
	lastReq map[string]any
}

// lastCall returns the headers and decoded body of the last System One call.
func (f *fakeAPI) lastCall() (http.Header, map[string]any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.last == nil {
		return nil, nil
	}
	return f.last.Header, f.lastReq
}

func (f *fakeAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.calls.Add(1)
	if s := f.status.Load(); s != 0 {
		w.WriteHeader(int(s))
		fmt.Fprint(w, `{"detail":"forced"}`)
		return
	}
	if r.URL.Path == "/v1/models" {
		fmt.Fprint(w, `{"models":[{"name":"jev-latest","description":"Flagship","release_date":"2026-10-01"}]}`)
		return
	}
	if f.jitter {
		time.Sleep(time.Duration(rand.IntN(5)) * time.Millisecond)
	}
	var req struct {
		State     any `json:"state"`
		Questions map[string]struct {
			Type     string          `json:"type"`
			Criteria json.RawMessage `json:"criteria"`
		} `json:"questions"`
	}
	body, _ := io.ReadAll(r.Body)
	if err := json.Unmarshal(body, &req); err != nil {
		w.WriteHeader(400)
		return
	}
	var generic map[string]any
	_ = json.Unmarshal(body, &generic)
	f.mu.Lock()
	f.last, f.lastReq = r, generic
	f.mu.Unlock()
	b, _ := json.Marshal(req.State)
	state := strings.ToLower(string(b))
	answers := map[string]any{}
	for id, q := range req.Questions {
		switch q.Type {
		case "noul":
			p := 0.05
			if strings.Contains(state, "401") || strings.Contains(state, "auth") {
				p = 0.95
			}
			answers[id] = map[string]any{"type": "noul", "noul": p}
		case "choice":
			var opts map[string]any
			_ = json.Unmarshal(q.Criteria, &opts)
			names := orderedKeys(q.Criteria)
			pick := names[len(names)-1]
			for _, n := range names {
				if strings.Contains(state, n) {
					pick = n
					break
				}
			}
			probs := map[string]float64{}
			for _, n := range names {
				probs[n] = 0.1 / float64(len(names)-1)
			}
			probs[pick] = 0.9
			conf := 0.9
			if strings.Contains(state, "?") {
				conf = 0.3
			}
			answers[id] = map[string]any{"type": "choice", "choice": pick, "probabilities": probs, "confidence": conf}
		case "score":
			var levels []any
			_ = json.Unmarshal(q.Criteria, &levels)
			idx := 0
			for i, l := range levels {
				if s, ok := l.(string); ok && strings.Contains(state, s) {
					idx = i
					break
				}
			}
			probs := map[string]float64{}
			for i := range levels {
				probs[strconv.Itoa(i)] = 0
			}
			probs[strconv.Itoa(idx)] = 1
			answers[id] = map[string]any{"type": "score", "score": float64(idx), "probabilities": probs, "confidence": 0.8}
		}
	}
	json.NewEncoder(w).Encode(map[string]any{
		"model": "jev-test", "answers": answers,
		"usage": map[string]int{"input_tokens": 10, "output_tokens": 1},
	})
}

func orderedKeys(raw json.RawMessage) []string {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.Token()
	var keys []string
	for d.More() {
		k, _ := d.Token()
		keys = append(keys, k.(string))
		var skip json.RawMessage
		d.Decode(&skip)
	}
	return keys
}

type harness struct {
	t           *testing.T
	api         *fakeAPI
	url         string
	env         map[string]string
	interactive bool
}

func newHarness(t *testing.T) *harness {
	api := &fakeAPI{}
	srv := httptest.NewServer(api)
	t.Cleanup(srv.Close)
	return &harness{t: t, api: api, url: srv.URL, env: map[string]string{
		"TYPESAFE_API_KEY": "test-key",
		"HUNCH_BASE_URL":   srv.URL,
		"HUNCH_CONFIG":     filepath.Join(t.TempDir(), "none.yaml"),
	}}
}

func (h *harness) run(stdin string, args ...string) (code int, stdout, stderr string) {
	var out, errOut bytes.Buffer
	e := &env{
		stdin:  strings.NewReader(stdin),
		stdout: &out,
		stderr: &errOut,
		getenv: func(k string) string { return h.env[k] },

		forceInteractive: h.interactive,
	}
	code = run(context.Background(), args, e)
	return code, out.String(), errOut.String()
}

const logs = `GET /health 200
POST /api/login 401 user=42

GET /static/app.js 200
POST /api/token 401 expired
`

func TestYesRaw(t *testing.T) {
	h := newHarness(t)
	code, out, errOut := h.run(logs, "Is this an authentication failure?", "--min", "0.8", "--raw")
	if code != 0 {
		t.Fatalf("code %d, stderr %s", code, errOut)
	}
	want := "POST /api/login 401 user=42\nPOST /api/token 401 expired\n"
	if out != want {
		t.Errorf("got %q", out)
	}
	if n := h.api.calls.Load(); n != 4 {
		t.Errorf("%d calls for 4 non-empty lines", n)
	}
}

func TestYesJSONL(t *testing.T) {
	h := newHarness(t)
	code, out, _ := h.run("401 /api/login\n", "yes", "auth?")
	if code != 0 || out != `{"line":"401 /api/login","hunch":{"p":0.95}}`+"\n" {
		t.Errorf("code %d out %q", code, out)
	}
}

func TestNoneKept(t *testing.T) {
	h := newHarness(t)
	code, out, _ := h.run(logs, "auth?", "--max", "0.01")
	if code != 1 || out != "" {
		t.Errorf("code %d out %q", code, out)
	}
}

func TestJSONLField(t *testing.T) {
	h := newHarness(t)
	in := `{"id":1,"body":"refund my billing please","n":12345678901234567890}
{"id":2,"body":"site is down, technical issue"}
{}
`
	// The third record has no .body: without --keep-going it stops the run.
	code, _, errOut := h.run(in, "pick", "Which team?", "-c", "billing,technical,sales", "--jsonpath", "$.body", "--key", "t")
	if code != 2 {
		t.Fatalf("code %d: %s", code, errOut)
	}
	code, out, errOut := h.run(in, "pick", "Which team?", "-c", "billing,technical,sales", "--jsonpath", "$.body", "--key", "t", "-k")
	if code != 5 || !strings.Contains(errOut, "record 3: nothing at $.body") {
		t.Fatalf("code %d: %s", code, errOut)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 2 {
		t.Fatalf("lines %q", lines)
	}
	if !strings.HasPrefix(lines[0], `{"id":1,"body":"refund my billing please","n":12345678901234567890,"t":{"choice":"billing","probs":{"billing":0.9,"technical":0.05,"sales":0.05},"confidence":0.9}}`) {
		t.Errorf("line 0: %s", lines[0])
	}
	if !strings.Contains(lines[1], `"choice":"technical"`) {
		t.Errorf("line 1: %s", lines[1])
	}
}

func TestMissingFieldStops(t *testing.T) {
	h := newHarness(t)
	code, _, errOut := h.run(`{"a":1}`+"\n", "auth?", "--jsonpath", ".b")
	if code != 2 || !strings.Contains(errOut, "nothing at .b") {
		t.Errorf("code %d: %s", code, errOut)
	}
}

func TestKeyClash(t *testing.T) {
	h := newHarness(t)
	code, _, errOut := h.run(`{"hunch":1}`+"\n", "auth?")
	if code != 2 || !strings.Contains(errOut, "--key") {
		t.Errorf("code %d: %s", code, errOut)
	}
}

func TestOrder(t *testing.T) {
	h := newHarness(t)
	h.api.jitter = true
	var in strings.Builder
	for i := range 200 {
		fmt.Fprintf(&in, "line %d\n", i)
	}
	code, out, _ := h.run(in.String(), "auth?", "-j", "16", "--raw")
	if code != 0 || out != in.String() {
		t.Errorf("order not preserved (code %d)", code)
	}
	code, out, _ = h.run(in.String(), "auth?", "-j", "16", "--raw", "--unordered")
	if code != 0 || len(strings.Split(out, "\n")) != 201 {
		t.Errorf("unordered lost lines (code %d)", code)
	}
}

func TestRouteAndConf(t *testing.T) {
	h := newHarness(t)
	dir := t.TempDir()
	in := "billing issue\ntechnical problem\nbilling question?\nhello\n"
	code, out, errOut := h.run(in, "pick", "team?", "-c", "billing", "-c", "technical", "-c", "sales=Pricing, upgrades",
		"--conf", "0.5", "-o", filepath.Join(dir, "{choice}.jsonl"))
	if code != 0 || out != "" {
		t.Fatalf("code %d out %q err %s", code, out, errOut)
	}
	read := func(name string) string {
		b, _ := os.ReadFile(filepath.Join(dir, name))
		return string(b)
	}
	if got := read("billing.jsonl"); strings.Count(got, "\n") != 1 || !strings.Contains(got, "billing issue") {
		t.Errorf("billing: %q", got)
	}
	if got := read("sales.jsonl"); !strings.Contains(got, "hello") {
		t.Errorf("sales: %q", got)
	}
	if got := read("_unsure.jsonl"); !strings.Contains(got, "billing question?") {
		t.Errorf("unsure: %q", got)
	}
}

func TestOnly(t *testing.T) {
	h := newHarness(t)
	code, out, _ := h.run("billing\ntechnical\n", "pick", "team?", "-c", "billing,technical", "--only", "technical", "--raw")
	if code != 0 || out != "technical\n" {
		t.Errorf("code %d out %q", code, out)
	}
	code, _, _ = h.run("x\n", "pick", "team?", "-c", "a,b", "--only", "c")
	if code != 2 {
		t.Errorf("--only with unknown option: code %d", code)
	}
}

func TestScoreTop(t *testing.T) {
	h := newHarness(t)
	in := "calm one\nfurious two\nannoyed three\nfurious four\n"
	code, out, _ := h.run(in, "score", "how angry?", "-l", "calm,annoyed,furious", "--top", "2", "--tsv")
	want := "2.00\tfurious\tfurious two\n2.00\tfurious\tfurious four\n"
	if code != 0 || out != want {
		t.Errorf("code %d out %q", code, out)
	}
}

func TestAsk(t *testing.T) {
	h := newHarness(t)
	f := filepath.Join(t.TempDir(), "q.yaml")
	os.WriteFile(f, []byte(`
questions:
  auth: { type: noul, q: "Is this an authentication failure?" }
  sev:  { type: choice, q: "Severity?", options: [low, medium, "high=Customer-facing"] }
  noise: { type: score, q: "Noise?", levels: [signal, noise] }
keep: "auth.p > 0.8 && sev.choice != 'low'"
`), 0o644)
	in := "401 low\n401 high\n200 high\n"
	code, out, errOut := h.run(in, "ask", "-f", f)
	if code != 0 {
		t.Fatalf("code %d: %s", code, errOut)
	}
	want := `{"line":"401 high","hunch":{"auth":{"p":0.95},"sev":{"choice":"high","probs":{"low":0.05,"medium":0.05,"high":0.9},"confidence":0.9},"noise":{"score":0,"level":"signal","probs":{"signal":1,"noise":0},"confidence":0.8}}}` + "\n"
	if out != want {
		t.Errorf("got  %s\nwant %s", out, want)
	}

	code, out, _ = h.run(in, "ask", "-f", f, "--keep", "sev.choice == 'low'", "--raw")
	if code != 0 || out != "401 low\n" {
		t.Errorf("--keep override: code %d out %q", code, out)
	}
}

func TestPreset(t *testing.T) {
	h := newHarness(t)
	cfg := filepath.Join(t.TempDir(), "config.yaml")
	os.WriteFile(cfg, []byte(`
providers:
  typesafe:
    model: jev-preview
presets:
  auth:
    questions:
      a: { type: noul, q: "auth?" }
    keep: a.p > 0.5
`), 0o644)
	h.env["HUNCH_CONFIG"] = cfg
	code, out, _ := h.run(logs, "@auth", "--raw")
	if code != 0 || strings.Count(out, "\n") != 2 {
		t.Errorf("code %d out %q", code, out)
	}
	code, _, errOut := h.run(logs, "@nope")
	if code != 2 || !strings.Contains(errOut, "no preset") {
		t.Errorf("code %d: %s", code, errOut)
	}
}

func TestWhole(t *testing.T) {
	h := newHarness(t)
	dir := t.TempDir()
	a, b := filepath.Join(dir, "a.diff"), filepath.Join(dir, "b.diff")
	os.WriteFile(a, []byte("+ password := \"hunter2\"\n+ // auth bypass\n"), 0o644)
	os.WriteFile(b, []byte("+ fmt.Println(1)\n"), 0o644)
	code, out, _ := h.run("", "--in", "whole", "secrets?", "--min", "0.5", "--raw", a, b)
	if code != 0 || out != a+"\n" {
		t.Errorf("code %d out %q", code, out)
	}
	code, _, _ = h.run("+ fmt.Println(1)\n", "--in", "whole", "secrets?", "--max", "0.3")
	if code != 0 {
		t.Errorf("gate: code %d", code)
	}
}

func TestDryRun(t *testing.T) {
	h := newHarness(t)
	delete(h.env, "TYPESAFE_API_KEY")
	code, out, _ := h.run("first\nsecond\n", "auth?", "--dry-run", "--context", "web logs", "--yes-when", "login failed")
	if code != 0 || h.api.calls.Load() != 0 {
		t.Fatalf("code %d calls %d", code, h.api.calls.Load())
	}
	var req map[string]any
	if err := json.Unmarshal([]byte(out), &req); err != nil {
		t.Fatal(err)
	}
	q := req["questions"].(map[string]any)["q"].(map[string]any)
	if req["state"] != "first" || req["model"] != "jev-latest" ||
		q["instructions"].(map[string]any)["context"] != "web logs" ||
		q["criteria"].(map[string]any)["true"] != "login failed" {
		t.Errorf("request %s", out)
	}
	if strings.Contains(out, "test-key") {
		t.Error("dry run printed the key")
	}
}

func TestErrors(t *testing.T) {
	h := newHarness(t)
	h.api.status.Store(401)
	if code, _, errOut := h.run(logs, "auth?"); code != 3 {
		t.Errorf("401: code %d %s", code, errOut)
	}
	h.api.status.Store(529)
	if code, _, errOut := h.run(logs, "auth?", "--retries", "0"); code != 4 {
		t.Errorf("529: code %d %s", code, errOut)
	}
	if code, _, _ := h.run(logs, "auth?", "--retries", "0", "-k"); code != 5 {
		t.Errorf("529 keep-going: code %d", code)
	}
	if code, _, _ := h.run(logs, "pick", "x?"); code != 2 {
		t.Errorf("missing -o: code %d", code)
	}
	if code, _, _ := h.run(logs, "auth?", "--bogus"); code != 2 {
		t.Errorf("unknown flag: code %d", code)
	}
	if code, _, _ := h.run(logs); code != 2 {
		t.Errorf("no args: code %d", code)
	}
}

func TestNoKey(t *testing.T) {
	keyring.MockInit()
	h := newHarness(t)
	delete(h.env, "TYPESAFE_API_KEY")
	if code, _, _ := h.run(logs, "auth?"); code != 3 {
		t.Errorf("no key: code %d", code)
	}
	if code, _, errOut := h.run("from-keychain-1234\n", "auth", "login"); code != 0 {
		t.Fatalf("login: code %d %s", code, errOut)
	}
	code, out, _ := h.run("", "auth", "status")
	if code != 0 || !strings.Contains(out, "…1234 from keychain") || !strings.Contains(out, "Provider  typesafe") {
		t.Errorf("status: code %d %q", code, out)
	}
	if code, _, _ := h.run(logs, "auth?", "--raw"); code != 0 {
		t.Errorf("keychain key: code %d", code)
	}
}

func TestStats(t *testing.T) {
	h := newHarness(t)
	_, _, errOut := h.run(logs, "auth?", "--stats", "--raw", "--min", "0.5")
	if !strings.Contains(errOut, "4 records, 2 kept, 0 failed, 4 calls, 40 input tokens") {
		t.Errorf("stats: %s", errOut)
	}
}

func TestParseLabels(t *testing.T) {
	ls, err := parseLabels([]string{"a,b", "billing=Payments, refunds"})
	if err != nil || len(ls) != 3 || ls[2].desc != "Payments, refunds" {
		t.Errorf("%v %+v", err, ls)
	}
	if _, err := parseLabels([]string{"a,a"}); err == nil {
		t.Error("duplicate accepted")
	}
}

func TestLoginWizard(t *testing.T) {
	keyring.MockInit()
	h := newHarness(t)
	delete(h.env, "TYPESAFE_API_KEY")
	cfg := filepath.Join(t.TempDir(), "hunch", "config.yaml")
	os.MkdirAll(filepath.Dir(cfg), 0o755)
	os.WriteFile(cfg, []byte("# my settings\njobs: 4\npresets:\n  a:\n    questions:\n      x: { type: noul, q: \"x?\" }\n"), 0o644)
	h.env["HUNCH_CONFIG"] = cfg
	h.interactive = true

	// Pick OpenRouter (2), paste a key, accept the default model.
	code, _, errOut := h.run("2\nsk-or-v1-secret-9876\n\n", "auth", "login")
	if code != 0 {
		t.Fatalf("login: code %d\n%s", code, errOut)
	}
	for _, want := range []string{"1) TypeSafe", "2) OpenRouter", "3) OpenCode Zen", "Model [~typesafe/jev-latest]", "ok, jev-test answered", "Saved provider openrouter"} {
		if !strings.Contains(errOut, want) {
			t.Errorf("wizard output lacks %q:\n%s", want, errOut)
		}
	}
	if strings.Contains(errOut, "secret-9876") {
		t.Error("wizard printed the key")
	}
	data, _ := os.ReadFile(cfg)
	for _, want := range []string{"# my settings", "jobs: 4", "presets:", "provider: openrouter", "model: ~typesafe/jev-latest"} {
		if !strings.Contains(string(data), want) {
			t.Errorf("config lacks %q:\n%s", want, data)
		}
	}

	// Later runs use the saved provider and the keychain key, no flags.
	h.interactive = false
	if code, _, errOut := h.run("401\n", "auth?", "--raw"); code != 0 {
		t.Fatalf("run: code %d %s", code, errOut)
	}
	hdr, req := h.api.lastCall()
	if hdr.Get("Authorization") != "Bearer sk-or-v1-secret-9876" || hdr.Get("X-Title") != "hunch" || req["model"] != "~typesafe/jev-latest" {
		t.Errorf("request auth=%q title=%q model=%v", hdr.Get("Authorization"), hdr.Get("X-Title"), req["model"])
	}
	_, out, _ := h.run("", "auth", "status")
	if !strings.Contains(out, "Provider  openrouter (OpenRouter)") || !strings.Contains(out, "…9876 from keychain") {
		t.Errorf("status: %s", out)
	}

	// Running the wizard again offers to keep the stored key.
	h.interactive = true
	code, _, errOut = h.run("\n\njev-custom\n", "auth", "login")
	if code != 0 || !strings.Contains(errOut, "Enter to keep …9876") {
		t.Fatalf("relogin: code %d\n%s", code, errOut)
	}
	data, _ = os.ReadFile(cfg)
	if !strings.Contains(string(data), "model: jev-custom") {
		t.Errorf("model not updated:\n%s", data)
	}
}

func TestLoginNonInteractive(t *testing.T) {
	keyring.MockInit()
	h := newHarness(t)
	h.env["HUNCH_CONFIG"] = filepath.Join(t.TempDir(), "config.yaml")
	h.env["OPENCODE_API_KEY"] = "zen-env-key-5555"
	// No key on stdin: the environment variable is used and not stored.
	code, _, errOut := h.run("", "auth", "login", "--provider", "zen")
	if code != 0 || strings.Contains(errOut, "Stored key") {
		t.Fatalf("code %d %s", code, errOut)
	}
	_, req := h.api.lastCall()
	if req["model"] != "jev-1.13" {
		t.Errorf("verify used model %v", req["model"])
	}
	_, out, _ := h.run("", "auth", "status")
	if !strings.Contains(out, "Provider  zen") || !strings.Contains(out, "from OPENCODE_API_KEY") {
		t.Errorf("status: %s", out)
	}

	h.api.status.Store(401)
	if code, _, _ := h.run("bad-key-0000\n", "auth", "login", "--provider", "typesafe"); code != 3 {
		t.Errorf("rejected key: code %d", code)
	}
	if code, _, _ := h.run("k\n", "auth", "login", "--provider", "nope"); code != 2 {
		t.Errorf("unknown provider: code %d", code)
	}
}

func TestProviderFlag(t *testing.T) {
	keyring.MockInit()
	h := newHarness(t)
	h.env["OPENROUTER_API_KEY"] = "or-key"
	if code, _, errOut := h.run("x\n", "auth?", "--provider", "openrouter", "--model", "typesafe/jev-1.13.0"); code != 0 {
		t.Fatalf("code %d %s", code, errOut)
	}
	hdr, req := h.api.lastCall()
	if hdr.Get("Authorization") != "Bearer or-key" || req["model"] != "typesafe/jev-1.13.0" {
		t.Errorf("auth %q model %v", hdr.Get("Authorization"), req["model"])
	}
	h.env["HUNCH_PROVIDER"] = "zen"
	code, _, errOut := h.run("x\n", "auth?")
	if code != 3 || !strings.Contains(errOut, "OPENCODE_API_KEY") {
		t.Errorf("zen without key: code %d %s", code, errOut)
	}
}

func TestHumanVerdict(t *testing.T) {
	p := &printer{spec: &spec{single: true}}
	for _, c := range []struct {
		p    float64
		want string
	}{{0.97, "yes   (0.97)  x"}, {0.5, "maybe (0.50)  x"}, {0.05, "no    (0.05)  x"}} {
		r := &result{rec: &record{mode: modeLines, raw: []byte("x")}, answers: []answer{{kind: "noul", p: c.p}}}
		if got := p.human(r); got != c.want {
			t.Errorf("p=%v: got %q, want %q", c.p, got, c.want)
		}
	}
}

func TestJSONPath(t *testing.T) {
	rec := map[string]any{
		"body":       "hi",
		"msg":        map[string]any{"text": "nested", "a.b": "dotted"},
		"items":      []any{map[string]any{"title": "first"}},
		"with space": "spaced",
	}
	for src, want := range map[string]any{
		"$.body":           "hi",
		".body":            "hi",
		"body":             "hi",
		"$.msg.text":       "nested",
		"$.items[0].title": "first",
		"$['with space']":  "spaced",
		`$.msg["a.b"]`:     "dotted",
		"$['msg']['text']": "nested",
	} {
		p, err := parseJSONPath(src)
		if err != nil {
			t.Errorf("%s: %v", src, err)
			continue
		}
		if got, err := p.get(rec); err != nil || got != want {
			t.Errorf("%s: got %v, %v", src, got, err)
		}
	}
	for _, src := range []string{"$", "$..body", "$.items[*]", "$.*", "$.items[x]", "$.a[", "$.a b"} {
		if _, err := parseJSONPath(src); err == nil {
			t.Errorf("%s: expected error", src)
		}
	}
	p, _ := parseJSONPath("$.items[3]")
	if _, err := p.get(rec); err == nil || !strings.Contains(err.Error(), "nothing at $.items[3]") {
		t.Errorf("out of range: %v", err)
	}
}

func TestOutputFile(t *testing.T) {
	h := newHarness(t)
	dir := t.TempDir()
	f := filepath.Join(dir, "sub", "kept.txt")
	code, out, errOut := h.run(logs, "auth?", "--min", "0.5", "--raw", "-o", f)
	if code != 0 || out != "" {
		t.Fatalf("code %d out %q err %s", code, out, errOut)
	}
	if b, _ := os.ReadFile(f); string(b) != "POST /api/login 401 user=42\nPOST /api/token 401 expired\n" {
		t.Errorf("file: %q", b)
	}
	// Nothing kept: the file still exists and is empty, like a redirect.
	empty := filepath.Join(dir, "empty.jsonl")
	if code, _, _ := h.run(logs, "auth?", "--min", "0.99", "-o", empty); code != 1 {
		t.Errorf("code %d", code)
	}
	if b, err := os.ReadFile(empty); err != nil || len(b) != 0 {
		t.Errorf("empty file: %q %v", b, err)
	}

	code, _, _ = h.run("calm\nfurious\n", "score", "angry?", "-l", "calm,furious", "-o", filepath.Join(dir, "by", "{level}.jsonl"))
	if b, _ := os.ReadFile(filepath.Join(dir, "by", "furious.jsonl")); code != 0 || !strings.Contains(string(b), `"level":"furious"`) {
		t.Errorf("score split: code %d %q", code, b)
	}

	for _, args := range [][]string{
		{"auth?", "-o", "out/{choice}.jsonl"},
		{"score", "x?", "-l", "a,b", "-o", "out/{choice}.jsonl"},
	} {
		if code, _, errOut := h.run(logs, args...); code != 2 || !strings.Contains(errOut, "-o:") {
			t.Errorf("%v: code %d %s", args, code, errOut)
		}
	}
}
