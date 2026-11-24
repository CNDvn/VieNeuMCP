# MCP stdio cho VieNeu

Binary `vieneu-mcp` nói MCP qua stdin/stdout. Cursor và Claude Code spawn process này, gọi tool, process gọi HTTP tới VieNeu đang host. Tool `speak` ghi một file WAV trên máy chạy agent và trả đường dẫn. Audio không đi vào context của model.

Tool của server tên `vieneu`:

| Tool | API |
|---|---|
| `health` | `GET /health` |
| `list_models` | `GET /v1/models` |
| `list_voices` | `GET /v1/voices` |
| `speak` | `POST /v1/audio/speech`, luôn xin `wav` |

`POST /v1/voices` không có. Route đó nhận file multipart và ghi giọng vào bộ nhớ của process server.

## 1. Cài Go

Cần Go **1.25** trở lên (`go 1.25.0` trong `mcp/go.mod`).

Kiểm tra:

```bash
go version
```

Chưa có thì tải bản cài cho đúng hệ điều hành tại [https://go.dev/dl/](https://go.dev/dl/).

- macOS: mở file `.pkg`, hoặc `brew install go`.
- Windows: chạy installer `.msi`. Mở PowerShell mới để `PATH` nhận `go`.
- Linux: giải nén vào `/usr/local/go` và thêm `/usr/local/go/bin` vào `PATH`.

`CGO_ENABLED=0` ở mọi lệnh build bên dưới để không cần gcc. Binary không gọi C.

Mọi lệnh `go` chạy trong thư mục `mcp` của repo này.

## 2. Build trên máy đang dùng

### macOS và Linux

```bash
cd mcp
go test ./...
CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o dist/vieneu-mcp .
./dist/vieneu-mcp -h
```

`-h` in cờ ra stderr rồi thoát. Process không in gì ra stdout. Khi Cursor chạy binary thật, nó đứng chờ khung MCP trên stdin; để nguyên như vậy, đừng gõ thêm.

### Windows (PowerShell)

```powershell
cd mcp
go test ./...
$env:CGO_ENABLED = "0"
go build -trimpath -ldflags "-s -w" -o dist/vieneu-mcp.exe .
.\dist\vieneu-mcp.exe -h
```

File ra là `mcp\dist\vieneu-mcp.exe`.

## 3. Build chéo

Chạy trên bất kỳ máy nào đã có Go. Đổi `GOOS` và `GOARCH`. Nhận binary ở `mcp/dist/`.

### macOS

```bash
cd mcp
CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -trimpath -ldflags "-s -w" -o dist/vieneu-mcp-darwin-arm64 .
CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 go build -trimpath -ldflags "-s -w" -o dist/vieneu-mcp-darwin-amd64 .
```

Apple silicon là `arm64`. Mac Intel là `amd64`.

### Linux

```bash
cd mcp
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags "-s -w" -o dist/vieneu-mcp-linux-amd64 .
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -ldflags "-s -w" -o dist/vieneu-mcp-linux-arm64 .
```

Trên máy đích:

```bash
chmod +x vieneu-mcp-linux-amd64
```

### Windows

PowerShell:

```powershell
cd mcp
$env:CGO_ENABLED = "0"
$env:GOOS = "windows"; $env:GOARCH = "amd64"
go build -trimpath -ldflags "-s -w" -o dist/vieneu-mcp-windows-amd64.exe .
$env:GOARCH = "arm64"
go build -trimpath -ldflags "-s -w" -o dist/vieneu-mcp-windows-arm64.exe .
Remove-Item Env:GOOS, Env:GOARCH
```

cmd.exe:

```bat
cd mcp
set CGO_ENABLED=0
set GOOS=windows
set GOARCH=amd64
go build -trimpath -ldflags "-s -w" -o dist\vieneu-mcp-windows-amd64.exe .
set GOARCH=arm64
go build -trimpath -ldflags "-s -w" -o dist\vieneu-mcp-windows-arm64.exe .
```

Đưa đúng file sang máy sẽ chạy agent. Đường dẫn trong cấu hình MCP phải là đường dẫn tuyệt đối trên máy đó.

## 4. Biến môi trường

Đặt trong cấu hình MCP, không ghi vào repo.

| Biến | Ý nghĩa |
|---|---|
| `VIENEU_BASE_URL` | Origin của server, không có path. Mặc định `http://127.0.0.1:8000`. Ví dụ `https://tts.example.com`. |
| `VIENEU_API_KEY` | Trùng `VIENEU_API_KEY` trên server. Server không đặt key thì bỏ biến này. |
| `VIENEU_MCP_OUT` | Thư mục ghi WAV. Mặc định là cache của user: macOS `~/Library/Caches/vieneu-mcp`, Linux `~/.cache/vieneu-mcp`, Windows `%LocalAppData%\vieneu-mcp`. |

Cùng nghĩa với cờ `-base`, `-api-key`, `-out` nếu chạy tay. Cursor và Claude Code dùng biến môi trường.

`VIENEU_BASE_URL` phải là URL cuối. Redirect sang host khác bị bỏ header `Authorization`.

## 5. Thêm vào Cursor

Tạo hoặc sửa file. Một project thì `.cursor/mcp.json` trong root project. Mọi project thì `~/.cursor/mcp.json` (Windows: `%USERPROFILE%\.cursor\mcp.json`).

macOS / Linux:

```json
{
  "mcpServers": {
    "vieneu": {
      "type": "stdio",
      "command": "/đường/dẫn/tuyệt/đối/vieneu-mcp",
      "env": {
        "VIENEU_BASE_URL": "https://your-vieneu-host",
        "VIENEU_API_KEY": "same-as-server"
      }
    }
  }
}
```

Windows, dùng dấu `/` cho đỡ phải escape `\`:

```json
{
  "mcpServers": {
    "vieneu": {
      "type": "stdio",
      "command": "C:/Users/you/bin/vieneu-mcp.exe",
      "env": {
        "VIENEU_BASE_URL": "https://your-vieneu-host",
        "VIENEU_API_KEY": "same-as-server"
      }
    }
  }
}
```

Cursor mở từ giao diện không đọc `.env` của shell. Key phải nằm trong `env` của file này.

Sau khi lưu: Cursor Settings, mục MCP, server `vieneu` phải xanh. Tắt rồi bật lại nếu nó vẫn báo lỗi cũ. Mở một chat mới.

Trong chat, hỏi: "Gọi tool health của server vieneu". Kết quả kỳ vọng là `status` bằng `ok` và tên backend. Tiếp theo: "Đọc câu Xin chào bằng VieNeu". Tool `speak` trả `path` tới một file `.wav`. Mở file đó bằng player của máy. Nội dung tool không chứa byte audio.

## 6. Thêm vào Claude Code

`command` là đường dẫn tuyệt đối. Mọi thứ sau `--` là lệnh của server.

Chỉ mình bạn, mọi project:

```bash
claude mcp add --transport stdio --scope user --env VIENEU_BASE_URL=https://your-vieneu-host --env VIENEU_API_KEY=same-as-server vieneu -- /đường/dẫn/tuyệt/đối/vieneu-mcp
```

Windows: thay đoạn cuối bằng `C:/Users/you/bin/vieneu-mcp.exe`. Không bọc bằng `cmd /c`. Đây là exe Go, không phải `npx`.

Chỉ project hiện tại, không commit:

```bash
claude mcp add --transport stdio --env VIENEU_BASE_URL=https://your-vieneu-host --env VIENEU_API_KEY=same-as-server vieneu -- /đường/dẫn/tuyệt/đối/vieneu-mcp
```

Đây là scope `local`, ghi trong `~/.claude.json` theo đường dẫn project.

Cả team dùng chung, ghi `.mcp.json` ở root repo. Đừng commit API key. Mỗi người export key ở máy mình, hoặc điền key sau khi clone và không đưa file đó vào git.

```bash
claude mcp add --transport stdio --scope project --env VIENEU_BASE_URL=https://your-vieneu-host vieneu -- /đường/dẫn/tuyệt/đối/vieneu-mcp
```

File tạo ra có dạng:

```json
{
  "mcpServers": {
    "vieneu": {
      "type": "stdio",
      "command": "/đường/dẫn/tuyệt/đối/vieneu-mcp",
      "env": {
        "VIENEU_BASE_URL": "https://your-vieneu-host"
      }
    }
  }
}
```

Lần đầu Claude Code hỏi approve server trong `.mcp.json`. Chấp nhận trong phiên `claude` tương tác.

Câu dài có thể chạy gần tới timeout 5 phút của `speak`. Nếu client cắt sớm, thêm `"timeout": 300000` (mili giây) vào entry server trong `.mcp.json`.

Kiểm tra:

```bash
claude mcp list
claude mcp get vieneu
```

Trạng thái phải là connected. Mở phiên mới rồi nhờ gọi `health`, sau đó `speak`.

Gỡ:

```bash
claude mcp remove vieneu --scope user
```

Đổi `--scope` cho đúng chỗ đã thêm.

## 7. Khi tool báo lỗi

| Lời tool trả về | Việc làm |
|---|---|
| `không kết nối được` | Sai `VIENEU_BASE_URL`, hoặc server VieNeu chưa chạy. `GET /health` bằng curl cùng URL đó. |
| `HTTP 401: invalid api key` | `VIENEU_API_KEY` hai bên không trùng. |
| `HTTP 400` kèm `unknown voice` | Gọi `list_voices` và dùng đúng `id` hoặc alias. |
| `HTTP 429` | Server hết slot stream. Process đã đợi `Retry-After` (tối đa 15 giây) và thử một lần. Vẫn 429 thì đợi rồi gọi `speak` lại. |
| `HTTP 503` hoặc `VieNeu không sẵn sàng` | Scheduler của VieNeu đã chết. Restart server. Gọi lại không hết. |
| Cursor báo server đỏ, log có dòng không phải JSON | `command` không trỏ tới đúng binary, hoặc stdout bị chương trình khác ghi. Binary này chỉ log ra stderr. |

File WAV do `speak` tạo có size RIFF đã ghi lại. Header streaming gốc của VieNeu để `0xFFFFFFFF` để player bắt đầu nghe sớm; file lưu xong thì size đó làm một số player từ chối mở.
