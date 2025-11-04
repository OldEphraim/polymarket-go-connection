package ws

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"time"
)

var (
	wsDebug       = os.Getenv("POLY_WS_DEBUG") == "1"
	wsLogUnknown  = os.Getenv("POLY_WS_LOG_UNKNOWN") == "1"
	wsUnknownPath = func() string {
		if p := os.Getenv("POLY_WS_UNKNOWN_PATH"); p != "" {
			return p
		}
		return "ws_unknown.jsonl"
	}()
)

func writeUnknown(kind string, raw []byte) {
	if !wsDebug && !wsLogUnknown {
		return
	}
	f, err := os.OpenFile(wsUnknownPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	entry := map[string]any{
		"ts":   time.Now().Format(time.RFC3339Nano),
		"kind": kind,
		"raw":  json.RawMessage(raw),
	}
	if b, err := json.Marshal(entry); err == nil {
		_, _ = f.Write(append(b, '\n'))
	}
}

func tryReadBody(resp *http.Response) string {
	if resp == nil || resp.Body == nil {
		return ""
	}
	defer resp.Body.Close()
	b, _ := ioReadAllN(resp.Body, 4096)
	return string(b)
}

func ioReadAllN(r io.ReadCloser, n int) ([]byte, error) {
	buf := make([]byte, 0, n)
	tmp := make([]byte, 1024)
	total := 0
	for total < n {
		k, err := r.Read(tmp)
		if k > 0 {
			buf = append(buf, tmp[:k]...)
			total += k
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return buf, nil
		}
	}
	return buf, nil
}
