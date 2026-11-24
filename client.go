package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	modelID       = "vieneu-v3-turbo"
	maxInputRunes = 20_000
	maxAudioBytes = 256 << 20
	healthTimeout = 10 * time.Second
	listTimeout   = 15 * time.Second
	speakTimeout  = 5 * time.Minute
	maxRetryWait  = 15 * time.Second
)

type config struct {
	BaseURL string
	APIKey  string
	OutDir  string
}

type client struct {
	base   string
	apiKey string
	outDir string
	http   *http.Client
}

func newClient(cfg config) (*client, error) {
	base, err := normalizeBase(cfg.BaseURL)
	if err != nil {
		return nil, err
	}
	out := cfg.OutDir
	if out == "" {
		cache, err := os.UserCacheDir()
		if err != nil {
			cache = os.TempDir()
		}
		out = filepath.Join(cache, "vieneu-mcp")
	}
	if err := os.MkdirAll(out, 0o755); err != nil {
		return nil, fmt.Errorf("tạo thư mục audio %s: %w", out, err)
	}
	return &client{
		base:   base,
		apiKey: strings.TrimSpace(cfg.APIKey),
		outDir: out,
		http: &http.Client{
			// Per-request context sets the deadline. A client-wide timeout would
			// cut a long utterance even when the caller asked for more time.
			CheckRedirect: dropAuthOnHostChange,
		},
	}, nil
}

func normalizeBase(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil {
		return "", fmt.Errorf("VIENEU_BASE_URL phải là http(s)://host[:port], không có path, nhận %q", raw)
	}
	if u.Path != "" && u.Path != "/" || u.RawQuery != "" || u.Fragment != "" {
		return "", fmt.Errorf("VIENEU_BASE_URL không được có path, query hay fragment, nhận %q", raw)
	}
	u.Path = ""
	return u.String(), nil
}

func dropAuthOnHostChange(req *http.Request, via []*http.Request) error {
	if len(via) >= 5 {
		return fmt.Errorf("quá 5 redirect từ %s", via[0].URL.Redacted())
	}
	if req.URL.Host != via[0].URL.Host {
		req.Header.Del("Authorization")
	}
	return nil
}

func (c *client) getJSON(ctx context.Context, path string, timeout time.Duration, dest any) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	resp, err := c.do(ctx, http.MethodGet, path, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return apiError(resp)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(dest); err != nil {
		return fmt.Errorf("đọc JSON %s: %w", path, err)
	}
	return nil
}

type speechRequest struct {
	Model             string   `json:"model"`
	Input             string   `json:"input"`
	Voice             string   `json:"voice,omitempty"`
	ResponseFormat    string   `json:"response_format"`
	StreamFormat      string   `json:"stream_format"`
	SampleRate        int      `json:"sample_rate,omitempty"`
	Temperature       *float64 `json:"temperature,omitempty"`
	TopK              *int     `json:"top_k,omitempty"`
	TopP              *float64 `json:"top_p,omitempty"`
	RepetitionPenalty *float64 `json:"repetition_penalty,omitempty"`
	MaxChars          *int     `json:"max_chars,omitempty"`
}

func (c *client) synthesize(ctx context.Context, in speakArgs) (speakOut, error) {
	if err := validateSpeak(in); err != nil {
		return speakOut{}, err
	}
	body := speechRequest{
		Model:             modelID,
		Input:             in.Input,
		Voice:             strings.TrimSpace(in.Voice),
		ResponseFormat:    "wav",
		StreamFormat:      "audio",
		SampleRate:        in.SampleRate,
		Temperature:       in.Temperature,
		TopK:              in.TopK,
		TopP:              in.TopP,
		RepetitionPenalty: in.RepetitionPenalty,
		MaxChars:          in.MaxChars,
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return speakOut{}, err
	}

	ctx, cancel := context.WithTimeout(ctx, speakTimeout)
	defer cancel()
	resp, err := c.do(ctx, http.MethodPost, "/v1/audio/speech", payload)
	if err != nil {
		return speakOut{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return speakOut{}, apiError(resp)
	}

	part, err := os.CreateTemp(c.outDir, ".vieneu-*.part")
	if err != nil {
		return speakOut{}, err
	}
	partName := part.Name()
	kept := false
	defer func() {
		if !kept {
			os.Remove(partName)
		}
	}()

	written, err := io.Copy(part, io.LimitReader(resp.Body, maxAudioBytes+1))
	closeErr := part.Close()
	if err != nil {
		return speakOut{}, fmt.Errorf("ghi audio: %w", err)
	}
	if closeErr != nil {
		return speakOut{}, closeErr
	}
	if written > maxAudioBytes {
		return speakOut{}, fmt.Errorf("audio lớn hơn %d byte, đã dừng", maxAudioBytes)
	}
	if written == 0 {
		return speakOut{}, fmt.Errorf("server trả body rỗng")
	}

	rate := in.SampleRate
	if h := resp.Header.Get("X-Sample-Rate"); h != "" {
		if n, err := strconv.Atoi(h); err == nil && n > 0 {
			rate = n
		}
	}
	if rate == 0 {
		rate = 48_000
	}
	dataBytes, err := patchWAV(partName)
	if err != nil {
		return speakOut{}, err
	}

	id := resp.Header.Get("X-Request-Id")
	final := wavPath(c.outDir, id)
	if err := os.Rename(partName, final); err != nil {
		return speakOut{}, err
	}
	kept = true

	out := speakOut{
		Path:       final,
		Voice:      body.Voice,
		Bytes:      written,
		SampleRate: rate,
		RequestID:  id,
	}
	if dataBytes > 0 {
		out.Seconds = float64(dataBytes) / 2 / float64(rate)
	}
	return out, nil
}

func validateSpeak(in speakArgs) error {
	n := len([]rune(strings.TrimSpace(in.Input)))
	if n < 1 || len([]rune(in.Input)) > maxInputRunes {
		return fmt.Errorf("input phải từ 1 đến %d ký tự", maxInputRunes)
	}
	switch in.SampleRate {
	case 0, 48_000, 24_000, 16_000, 8_000:
	default:
		return fmt.Errorf("sample_rate phải là 48000, 24000, 16000 hoặc 8000")
	}
	if in.Temperature != nil && (*in.Temperature < 0 || *in.Temperature > 2) {
		return fmt.Errorf("temperature phải từ 0 đến 2")
	}
	if in.TopK != nil && (*in.TopK < 1 || *in.TopK > 1024) {
		return fmt.Errorf("top_k phải từ 1 đến 1024")
	}
	if in.TopP != nil && (*in.TopP <= 0 || *in.TopP > 1) {
		return fmt.Errorf("top_p phải lớn hơn 0 và không quá 1")
	}
	if in.RepetitionPenalty != nil && (*in.RepetitionPenalty < 1 || *in.RepetitionPenalty > 2) {
		return fmt.Errorf("repetition_penalty phải từ 1 đến 2")
	}
	if in.MaxChars != nil && (*in.MaxChars < 64 || *in.MaxChars > 512) {
		return fmt.Errorf("max_chars phải từ 64 đến 512")
	}
	return nil
}

func (c *client) do(ctx context.Context, method, path string, payload []byte) (*http.Response, error) {
	var last error
	for attempt := 0; attempt < 2; attempt++ {
		resp, err := c.roundTrip(ctx, method, path, payload)
		if err != nil {
			return nil, err
		}
		if resp.StatusCode != http.StatusTooManyRequests || attempt == 1 {
			return resp, nil
		}
		wait := retryAfter(resp)
		resp.Body.Close()
		last = fmt.Errorf("HTTP 429, đợi %s rồi thử lại một lần", wait)
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
	return nil, last
}

func (c *client) roundTrip(ctx context.Context, method, path string, payload []byte) (*http.Response, error) {
	var body io.Reader
	if payload != nil {
		body = bytes.NewReader(payload)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, body)
	if err != nil {
		return nil, err
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("không kết nối được %s: %w", c.base, err)
	}
	return resp, nil
}

func apiError(resp *http.Response) error {
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<10))
	var obj map[string]json.RawMessage
	if json.Unmarshal(raw, &obj) == nil {
		if e, ok := obj["error"]; ok {
			var asObj struct {
				Message string `json:"message"`
			}
			if json.Unmarshal(e, &asObj) == nil && asObj.Message != "" {
				return fmt.Errorf("HTTP %d: %s", resp.StatusCode, asObj.Message)
			}
			var asStr string
			if json.Unmarshal(e, &asStr) == nil && asStr != "" {
				return fmt.Errorf("HTTP %d: %s", resp.StatusCode, asStr)
			}
		}
	}
	msg := strings.TrimSpace(string(raw))
	if msg == "" {
		msg = resp.Status
	}
	return fmt.Errorf("HTTP %d: %s", resp.StatusCode, msg)
}

func retryAfter(resp *http.Response) time.Duration {
	n, err := strconv.Atoi(strings.TrimSpace(resp.Header.Get("Retry-After")))
	if err != nil || n < 0 {
		return time.Second
	}
	if n > int(maxRetryWait/time.Second) {
		n = int(maxRetryWait / time.Second)
	}
	return time.Duration(n) * time.Second
}

func wavPath(dir, requestID string) string {
	id := sanitize(requestID)
	if id == "" {
		id = randHex(8)
	}
	path := filepath.Join(dir, "vieneu-"+id+".wav")
	if _, err := os.Stat(path); err == nil {
		path = filepath.Join(dir, "vieneu-"+id+"-"+randHex(4)+".wav")
	}
	return path
}

func sanitize(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		}
	}
	if b.Len() > 64 {
		return b.String()[:64]
	}
	return b.String()
}

func randHex(n int) string {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(buf)
}
