package typesafe

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"
)

func TestQuestionJSON(t *testing.T) {
	qs := Questions{
		"a": Noul("Is it?").WithCriteria("yes means", nil),
		"b": ChoiceOf("Which?", Opt("zeta", "last letter"), Opt("alpha", nil)),
		"c": Score("How much?", "low", map[string]string{"name": "high"}),
	}
	got, err := json.Marshal(qs)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"a":{"type":"noul","instructions":"Is it?","criteria":{"true":"yes means"}},` +
		`"b":{"type":"choice","instructions":"Which?","criteria":{"zeta":"last letter","alpha":null}},` +
		`"c":{"type":"score","instructions":"How much?","criteria":["low",{"name":"high"}]}}`
	if string(got) != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

func TestValidate(t *testing.T) {
	for name, q := range map[string]Question{
		"one option":  Choice("q", "a"),
		"dup option":  Choice("q", "a", "a"),
		"one level":   Score("q", "a"),
		"many levels": Score("q", "1", "2", "3", "4", "5", "6", "7", "8", "9", "10", "11"),
	} {
		if err := q.Validate(); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
	if err := (Request{}).Validate(); err == nil {
		t.Error("empty request: expected error")
	}
}

func TestAsk(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/systemone" || r.Header.Get("Authorization") != "Bearer k" {
			t.Errorf("bad request %s %s", r.URL.Path, r.Header.Get("Authorization"))
		}
		var req map[string]any
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req["model"] != "jev-latest" || req["state"] != "hello" {
			t.Errorf("bad body %v", req)
		}
		io.WriteString(w, `{"model":"jev-1.13.0","answers":{"x":{"type":"noul","noul":0.9},
			"s":{"type":"score","score":1.4,"probabilities":{"0":0.1,"1":0.4,"2":0.5},"confidence":0.3}},
			"usage":{"input_tokens":10,"output_tokens":2}}`)
	}))
	defer srv.Close()

	c := NewClient(WithAPIKey("k"), WithBaseURL(srv.URL), WithModel(""))
	res, err := c.Ask(context.Background(), Request{
		State:     "hello",
		Questions: Questions{"x": Noul("?"), "s": Score("?", "a", "b", "c")},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Answers["x"].Noul != 0.9 || res.Model != "jev-1.13.0" || res.Usage.InputTokens != 10 {
		t.Errorf("bad response %+v", res)
	}
	if s := res.Answers["s"]; s.Level() != 1 || s.LevelProbability(2) != 0.5 {
		t.Errorf("bad score %+v", s)
	}
}

func TestCompression(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Content-Encoding") != "zstd" {
			t.Error("large body not compressed")
		}
		dec, _ := zstd.NewReader(r.Body)
		var req Request
		if err := json.NewDecoder(dec).Decode(&struct{ State *any }{&req.State}); err != nil {
			t.Error(err)
		}
		if s, _ := req.State.(string); len(s) != 4096 {
			t.Errorf("state length %d", len(s))
		}
		io.WriteString(w, `{"answers":{}}`)
	}))
	defer srv.Close()

	c := NewClient(WithAPIKey("k"), WithBaseURL(srv.URL))
	_, err := c.Ask(context.Background(), Request{State: strings.Repeat("a", 4096), Questions: Questions{"x": Noul("?")}})
	if err != nil {
		t.Fatal(err)
	}
}

func TestRetries(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) < 3 {
			w.Header().Set("Retry-After-Ms", "1")
			w.WriteHeader(529)
			return
		}
		io.WriteString(w, `{"answers":{"x":{"type":"noul","noul":1}}}`)
	}))
	defer srv.Close()

	var hooks int
	c := NewClient(WithAPIKey("k"), WithBaseURL(srv.URL), WithRetryHook(func(int, error, time.Duration) { hooks++ }))
	if _, err := c.Ask(context.Background(), Request{State: "s", Questions: Questions{"x": Noul("?")}}); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 3 || hooks != 2 {
		t.Errorf("calls=%d hooks=%d", calls.Load(), hooks)
	}
}

func TestErrors(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(401)
		io.WriteString(w, `{"detail":"invalid api key"}`)
	}))
	defer srv.Close()

	c := NewClient(WithAPIKey("k"), WithBaseURL(srv.URL))
	_, err := c.Ask(context.Background(), Request{State: "s", Questions: Questions{"x": Noul("?")}})
	if !IsAuthError(err) || !strings.Contains(err.Error(), "invalid api key") {
		t.Errorf("got %v", err)
	}
	if calls.Load() != 1 {
		t.Errorf("401 retried: %d calls", calls.Load())
	}

	_, err = c.Ask(context.Background(), Request{State: strings.Repeat("a", MaxBodySize), Questions: Questions{"x": Noul("?")}})
	if !errors.Is(err, ErrRequestTooLarge) {
		t.Errorf("got %v", err)
	}

	t.Setenv(EnvAPIKey, "")
	_, err = NewClient(WithBaseURL(srv.URL)).ListModels(context.Background())
	if !errors.Is(err, ErrNoAPIKey) {
		t.Errorf("got %v", err)
	}
}
