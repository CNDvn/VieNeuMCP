package main

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestNormalizeBase(t *testing.T) {
	got, err := normalizeBase("http://127.0.0.1:8000/")
	if err != nil || got != "http://127.0.0.1:8000" {
		t.Fatalf("got %q err %v", got, err)
	}
	for _, bad := range []string{"", "ftp://x", "http://host/v1", "https://user:pw@host"} {
		if _, err := normalizeBase(bad); err == nil {
			t.Fatalf("accepted %q", bad)
		}
	}
}

func TestValidateSpeak(t *testing.T) {
	if err := validateSpeak(speakArgs{Input: "xin chào"}); err != nil {
		t.Fatal(err)
	}
	if err := validateSpeak(speakArgs{Input: "  "}); err == nil {
		t.Fatal("blank input accepted")
	}
	if err := validateSpeak(speakArgs{Input: "a", SampleRate: 22050}); err == nil {
		t.Fatal("odd sample rate accepted")
	}
}

func TestPatchWAV(t *testing.T) {
	path := t.TempDir() + "/a.wav"
	pcm := []byte{1, 2, 3, 4}
	hdr := make([]byte, 44)
	copy(hdr[0:], "RIFF")
	binary.LittleEndian.PutUint32(hdr[4:], 0xFFFFFFFF)
	copy(hdr[8:], "WAVE")
	copy(hdr[36:], "data")
	binary.LittleEndian.PutUint32(hdr[40:], 0xFFFFFFFF)
	if err := os.WriteFile(path, append(hdr, pcm...), 0o644); err != nil {
		t.Fatal(err)
	}
	n, err := patchWAV(path)
	if err != nil || n != int64(len(pcm)) {
		t.Fatalf("dataBytes=%d err=%v", n, err)
	}
	buf, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := binary.LittleEndian.Uint32(buf[4:]); got != uint32(len(buf)-8) {
		t.Fatalf("riff size %d", got)
	}
	if got := binary.LittleEndian.Uint32(buf[40:]); got != uint32(len(pcm)) {
		t.Fatalf("data size %d", got)
	}
}

func TestSpeakWritesWAVAndKeepsItOutOfTheToolText(t *testing.T) {
	var sawAuth string
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		sawAuth = r.Header.Get("Authorization")
		if r.URL.Path != "/v1/audio/speech" || r.Method != http.MethodPost {
			t.Errorf("path %s %s", r.Method, r.URL.Path)
		}
		var body speechRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if body.Input != "Xin chào" || body.Voice != "Binh" || body.ResponseFormat != "wav" || body.StreamFormat != "audio" {
			t.Errorf("body %+v", body)
		}
		if body.Temperature != nil {
			t.Error("unset temperature was sent")
		}
		w.Header().Set("X-Request-Id", "spk-test")
		w.Header().Set("X-Sample-Rate", "48000")
		hdr := make([]byte, 44)
		copy(hdr, "RIFF")
		binary.LittleEndian.PutUint32(hdr[4:], 0xFFFFFFFF)
		copy(hdr[8:], "WAVEfmt ")
		copy(hdr[36:], "data")
		binary.LittleEndian.PutUint32(hdr[40:], 0xFFFFFFFF)
		w.Write(append(hdr, 0, 0, 0, 0))
	}))
	defer srv.Close()

	session := connect(t, srv.URL, "secret")
	res, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "speak",
		Arguments: map[string]any{"input": "Xin chào", "voice": "Binh"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("tool error: %+v", res.Content)
	}
	if sawAuth != "Bearer secret" {
		t.Fatalf("auth %q", sawAuth)
	}
	raw, err := json.Marshal(res.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	var out speakOut
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(out.Path, "vieneu-spk-test.wav") || out.Bytes != 48 || out.SampleRate != 48000 || out.Seconds == 0 {
		t.Fatalf("out %+v", out)
	}
	text := res.Content[0].(*mcp.TextContent).Text
	if strings.Contains(text, "RIFF") || len(text) > 2000 {
		t.Fatalf("tool text looks like audio: %s", text)
	}
	if _, err := os.Stat(out.Path); err != nil {
		t.Fatal(err)
	}
}

func TestSpeakToolErrorOnUnauthorized(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error":{"message":"invalid api key","type":"invalid_request_error","code":401}}`))
	}))
	defer srv.Close()

	session := connect(t, srv.URL, "")
	res, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "speak",
		Arguments: map[string]any{"input": "a"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatal("expected tool error")
	}
	text := res.Content[0].(*mcp.TextContent).Text
	if !strings.Contains(text, "invalid api key") {
		t.Fatalf("text %s", text)
	}
}

func TestSpeakRetries429Once(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			w.Write([]byte(`{"error":{"message":"busy"}}`))
			return
		}
		w.Header().Set("X-Request-Id", "spk-retry")
		w.Header().Set("X-Sample-Rate", "8000")
		hdr := make([]byte, 44)
		copy(hdr, "RIFF")
		copy(hdr[8:], "WAVE")
		copy(hdr[36:], "data")
		w.Write(append(hdr, 1, 2))
	}))
	defer srv.Close()

	session := connect(t, srv.URL, "")
	res, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "speak",
		Arguments: map[string]any{"input": "a", "sample_rate": 8000},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError || calls != 2 {
		t.Fatalf("isError=%v calls=%d content=%v", res.IsError, calls, res.Content)
	}
}

func TestHealth(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			if r.Header.Get("Authorization") != "" {
				t.Error("health sent an empty-key authorization")
			}
			w.Write([]byte(`{"status":"ok","backend":"onnx","max_streams":2,"active":0,"sample_rate":48000}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	session := connect(t, srv.URL, "")
	res, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "health"})
	if err != nil || res.IsError {
		t.Fatalf("err=%v res=%+v", err, res)
	}
	raw, _ := json.Marshal(res.StructuredContent)
	var out healthOut
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	if out.Status != "ok" || out.Backend != "onnx" {
		t.Fatalf("%+v", out)
	}
}

func TestSpeakSchemaRequiresOnlyInput(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"status":"ok"}`))
	}))
	defer srv.Close()
	session := connect(t, srv.URL, "")
	list, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var speak *mcp.Tool
	for _, tool := range list.Tools {
		if tool.Name == "speak" {
			speak = tool
		}
	}
	if speak == nil || speak.InputSchema == nil {
		t.Fatal("speak schema missing")
	}
	raw, err := json.Marshal(speak.InputSchema)
	if err != nil {
		t.Fatal(err)
	}
	var schema struct {
		Required []string `json:"required"`
	}
	if err := json.Unmarshal(raw, &schema); err != nil {
		t.Fatal(err)
	}
	if len(schema.Required) != 1 || schema.Required[0] != "input" {
		t.Fatalf("required %v schema %s", schema.Required, raw)
	}
}

func connect(t *testing.T, base, key string) *mcp.ClientSession {
	t.Helper()
	c, err := newClient(config{BaseURL: base, APIKey: key, OutDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	server := newServer(c)
	clientTransport, serverTransport := mcp.NewInMemoryTransports()
	if _, err := server.Connect(context.Background(), serverTransport, nil); err != nil {
		t.Fatal(err)
	}
	session, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "v0"}, nil).Connect(context.Background(), clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { session.Close() })
	return session
}
