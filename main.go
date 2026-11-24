// Command vieneu-mcp is a stdio MCP server that calls a running VieNeu speech API.
//
// Logs go to stderr. Stdout is reserved for MCP frames; a single log line there
// makes Cursor and Claude Code drop the server.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func main() {
	log.SetOutput(os.Stderr)
	log.SetFlags(log.LstdFlags)

	cfg, err := configFromFlags()
	if err != nil {
		log.Fatal(err)
	}
	client, err := newClient(cfg)
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("vieneu-mcp stdio base=%s out=%s auth=%t", client.base, client.outDir, client.apiKey != "")
	if err := newServer(client).Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		log.Fatal(err)
	}
}

func configFromFlags() (config, error) {
	fs := flag.NewFlagSet("vieneu-mcp", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	base := fs.String("base", getenv("VIENEU_BASE_URL", "http://127.0.0.1:8000"), "VieNeu origin, no path (env VIENEU_BASE_URL)")
	key := fs.String("api-key", os.Getenv("VIENEU_API_KEY"), "Bearer token; empty if the server has no VIENEU_API_KEY")
	out := fs.String("out", os.Getenv("VIENEU_MCP_OUT"), "directory for wav files (env VIENEU_MCP_OUT)")
	if err := fs.Parse(os.Args[1:]); err != nil {
		if err == flag.ErrHelp {
			os.Exit(0)
		}
		return config{}, err
	}
	return config{BaseURL: *base, APIKey: *key, OutDir: *out}, nil
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func newServer(c *client) *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: "vieneu", Version: "1.0.0"}, nil)
	mcp.AddTool(s, &mcp.Tool{
		Name:        "health",
		Description: "Kiểm tra server VieNeu còn nhận request không. status=ok là đọc được. Lỗi báo scheduler chết thì phải restart server; gọi lại không hết.",
	}, c.health)
	mcp.AddTool(s, &mcp.Tool{
		Name:        "list_models",
		Description: "Mô tả model đang chạy: id, sample rate gốc, backend, số stream song song, format và sample rate được phép.",
	}, c.listModels)
	mcp.AddTool(s, &mcp.Tool{
		Name:        "list_voices",
		Description: "Liệt kê giọng trên server này. id hoặc alias đưa vào speak.voice. Bỏ voice khi gọi speak để dùng giọng mặc định (thường là Binh).",
	}, c.listVoices)
	mcp.AddTool(s, &mcp.Tool{
		Name:        "speak",
		Description: "Đọc một đoạn tiếng Việt và ghi một file WAV trên máy đang chạy agent. Kết quả là đường dẫn file, số byte và thời lượng. Không trả audio. input là câu cần đọc, tối đa 20000 ký tự. voice lấy từ list_voices.",
	}, c.speak)
	return s
}

type emptyArgs struct{}

type healthOut struct {
	Status     string `json:"status"`
	Backend    string `json:"backend,omitempty"`
	MaxStreams int    `json:"max_streams,omitempty"`
	Active     int    `json:"active,omitempty"`
	Waiting    int    `json:"waiting,omitempty"`
	SampleRate int    `json:"sample_rate,omitempty"`
	Error      string `json:"error,omitempty"`
}

type listModelsOut struct {
	Object string      `json:"object"`
	Data   []modelInfo `json:"data"`
}

type modelInfo struct {
	ID              string   `json:"id"`
	OwnedBy         string   `json:"owned_by,omitempty"`
	SampleRate      int      `json:"sample_rate,omitempty"`
	Backend         string   `json:"backend,omitempty"`
	MaxStreams      int      `json:"max_streams,omitempty"`
	ResponseFormats []string `json:"response_formats,omitempty"`
	StreamFormats   []string `json:"stream_formats,omitempty"`
	SampleRates     []int    `json:"sample_rates,omitempty"`
}

type listVoicesOut struct {
	Object string      `json:"object"`
	Data   []voiceInfo `json:"data"`
}

type voiceInfo struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	Gender      string   `json:"gender,omitempty"`
	Aliases     []string `json:"aliases,omitempty"`
}

type speakArgs struct {
	Input             string   `json:"input" jsonschema:"Câu tiếng Việt cần đọc, từ 1 đến 20000 ký tự."`
	Voice             string   `json:"voice,omitempty" jsonschema:"id hoặc alias từ list_voices. Bỏ trống để dùng giọng mặc định."`
	SampleRate        int      `json:"sample_rate,omitempty" jsonschema:"48000 (mặc định), 24000, 16000 hoặc 8000."`
	Temperature       *float64 `json:"temperature,omitempty" jsonschema:"0 đến 2. Bỏ trống để dùng 0.8."`
	TopK              *int     `json:"top_k,omitempty" jsonschema:"1 đến 1024. Bỏ trống để dùng 25."`
	TopP              *float64 `json:"top_p,omitempty" jsonschema:"lớn hơn 0 và không quá 1. Bỏ trống để dùng 0.95."`
	RepetitionPenalty *float64 `json:"repetition_penalty,omitempty" jsonschema:"1 đến 2. Bỏ trống để dùng 1.2."`
	MaxChars          *int     `json:"max_chars,omitempty" jsonschema:"64 đến 512, độ dài mỗi đoạn nội bộ. Bỏ trống để dùng 256."`
}

type speakOut struct {
	Path       string  `json:"path" jsonschema:"Đường dẫn file WAV trên máy đang chạy agent."`
	Voice      string  `json:"voice,omitempty" jsonschema:"Giọng đã yêu cầu. Rỗng nghĩa là giọng mặc định của server."`
	Bytes      int64   `json:"bytes" jsonschema:"Kích thước file."`
	SampleRate int     `json:"sample_rate" jsonschema:"Sample rate của file."`
	Seconds    float64 `json:"seconds,omitempty" jsonschema:"Thời lượng ước tính."`
	RequestID  string  `json:"request_id,omitempty" jsonschema:"X-Request-Id của server VieNeu."`
}

func (c *client) health(ctx context.Context, _ *mcp.CallToolRequest, _ emptyArgs) (*mcp.CallToolResult, healthOut, error) {
	var out healthOut
	if err := c.getJSON(ctx, "/health", healthTimeout, &out); err != nil {
		return nil, healthOut{}, err
	}
	if out.Status == "error" {
		msg := out.Error
		if msg == "" {
			msg = "speech engine is down"
		}
		return nil, healthOut{}, fmt.Errorf("VieNeu không sẵn sàng: %s", msg)
	}
	return nil, out, nil
}

func (c *client) listModels(ctx context.Context, _ *mcp.CallToolRequest, _ emptyArgs) (*mcp.CallToolResult, listModelsOut, error) {
	var out listModelsOut
	if err := c.getJSON(ctx, "/v1/models", listTimeout, &out); err != nil {
		return nil, listModelsOut{}, err
	}
	return nil, out, nil
}

func (c *client) listVoices(ctx context.Context, _ *mcp.CallToolRequest, _ emptyArgs) (*mcp.CallToolResult, listVoicesOut, error) {
	var out listVoicesOut
	if err := c.getJSON(ctx, "/v1/voices", listTimeout, &out); err != nil {
		return nil, listVoicesOut{}, err
	}
	return nil, out, nil
}

func (c *client) speak(ctx context.Context, _ *mcp.CallToolRequest, in speakArgs) (*mcp.CallToolResult, speakOut, error) {
	out, err := c.synthesize(ctx, in)
	if err != nil {
		return nil, speakOut{}, err
	}
	return nil, out, nil
}
