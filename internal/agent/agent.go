package agent

import (
	"cloudbrowser/internal/auth"
	"cloudbrowser/internal/protocol"
	"context"
	"encoding/json"
	"fmt"
	"github.com/coder/websocket"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type Agent struct {
	Token, Home string
	mu          sync.Mutex
	chooserMu   sync.Mutex
	choosers    map[string]*fileChooser
}
type record struct {
	ID     string `json:"id"`
	Target string `json:"target"`
	URL    string `json:"url"`
	State  string `json:"state"`
}
type target struct {
	ID        string `json:"id"`
	URL       string `json:"url"`
	WebSocket string `json:"webSocketDebuggerUrl"`
}
type fileChooser struct {
	connection    *websocket.Conn
	backendNodeID int64
	expires       time.Time
}

func (a *Agent) Handler() http.Handler {
	m := http.NewServeMux()
	m.HandleFunc("GET /healthz", a.health)
	m.HandleFunc("POST /open", a.open)
	m.HandleFunc("GET /operations/{id}", a.operation)
	m.HandleFunc("GET /audio", a.audio)
	m.HandleFunc("POST /input", a.input)
	m.HandleFunc("GET /file-chooser", a.waitForFileChooser)
	m.HandleFunc("POST /file-chooser/{id}", a.completeFileChooser)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !auth.Equal(r.Header.Get("Authorization"), "Bearer "+a.Token) || a.Token == "" {
			protocol.Error(w, 403, "FORBIDDEN")
			return
		}
		m.ServeHTTP(w, r)
	})
}
func get(ctx context.Context, path string, out any) error {
	q, _ := http.NewRequestWithContext(ctx, "GET", "http://127.0.0.1:9222"+path, nil)
	res, e := (&http.Client{Timeout: 3 * time.Second}).Do(q)
	if e != nil {
		return e
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return fmt.Errorf("CDP_UNAVAILABLE")
	}
	return json.NewDecoder(io.LimitReader(res.Body, 4<<20)).Decode(out)
}
func call(ctx context.Context, socket, method string, params any) (json.RawMessage, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	c, _, e := websocket.Dial(ctx, socket, nil)
	if e != nil {
		return nil, e
	}
	defer c.CloseNow()
	c.SetReadLimit(4 << 20)
	b, _ := json.Marshal(map[string]any{"id": 1, "method": method, "params": params})
	if e = c.Write(ctx, websocket.MessageText, b); e != nil {
		return nil, e
	}
	for {
		_, b, e = c.Read(ctx)
		if e != nil {
			return nil, e
		}
		var msg struct {
			ID     int
			Result json.RawMessage
			Error  json.RawMessage
		}
		if json.Unmarshal(b, &msg) != nil {
			continue
		}
		if msg.ID == 1 {
			if len(msg.Error) > 0 {
				return nil, fmt.Errorf("CDP_COMMAND_FAILED")
			}
			return msg.Result, nil
		}
	}
}
func browserCall(ctx context.Context, method string, params any) (json.RawMessage, error) {
	var v struct {
		WebSocket string `json:"webSocketDebuggerUrl"`
	}
	if e := get(ctx, "/json/version", &v); e != nil {
		return nil, e
	}
	return call(ctx, v.WebSocket, method, params)
}
func activeTarget(ctx context.Context) (target, error) {
	var targets []target
	if err := get(ctx, "/json/list", &targets); err != nil {
		return target{}, err
	}
	var fallback target
	for _, candidate := range targets {
		if candidate.WebSocket == "" || strings.HasPrefix(candidate.URL, "chrome://") {
			continue
		}
		if fallback.ID == "" {
			fallback = candidate
		}
		result, err := call(ctx, candidate.WebSocket, "Runtime.evaluate", map[string]any{
			"expression":    "document.hasFocus()",
			"returnByValue": true,
		})
		if err != nil {
			continue
		}
		var evaluated struct {
			Result struct {
				Value bool `json:"value"`
			} `json:"result"`
		}
		if json.Unmarshal(result, &evaluated) == nil && evaluated.Result.Value {
			return candidate, nil
		}
	}
	if fallback.ID != "" {
		return fallback, nil
	}
	return target{}, fmt.Errorf("ACTIVE_TARGET_UNAVAILABLE")
}

func cdpCommand(ctx context.Context, connection *websocket.Conn, id int, method string, params any) error {
	payload, _ := json.Marshal(map[string]any{"id": id, "method": method, "params": params})
	if err := connection.Write(ctx, websocket.MessageText, payload); err != nil {
		return err
	}
	for {
		_, data, err := connection.Read(ctx)
		if err != nil {
			return err
		}
		var message struct {
			ID    int             `json:"id"`
			Error json.RawMessage `json:"error"`
		}
		if json.Unmarshal(data, &message) != nil || message.ID != id {
			continue
		}
		if len(message.Error) > 0 {
			return fmt.Errorf("CDP_COMMAND_FAILED")
		}
		return nil
	}
}
func (a *Agent) health(w http.ResponseWriter, r *http.Request) {
	var t []target
	if get(r.Context(), "/json/list", &t) != nil {
		protocol.Error(w, 503, "CHROME_NOT_READY")
		return
	}
	ctx, c := context.WithTimeout(r.Context(), 2*time.Second)
	defer c()
	if exec.CommandContext(ctx, "xdpyinfo").Run() != nil {
		protocol.Error(w, 503, "DISPLAY_NOT_READY")
		return
	}
	protocol.JSON(w, 200, map[string]bool{"ok": true})
}
func (a *Agent) dir() string { return filepath.Join(a.Home, ".cloud-browser", "operations") }
func (a *Agent) load(id string) (record, error) {
	var v record
	b, e := os.ReadFile(filepath.Join(a.dir(), id+".json"))
	if e != nil {
		return v, e
	}
	e = json.Unmarshal(b, &v)
	return v, e
}
func (a *Agent) save(v record) error {
	if e := os.MkdirAll(a.dir(), 0700); e != nil {
		return e
	}
	b, _ := json.Marshal(v)
	path := filepath.Join(a.dir(), v.ID+".json")
	f, e := os.OpenFile(path+".tmp", os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if e != nil {
		return e
	}
	if _, e = f.Write(b); e == nil {
		e = f.Sync()
	}
	f.Close()
	if e != nil {
		return e
	}
	return os.Rename(path+".tmp", path)
}
func (a *Agent) recover(ctx context.Context, v record) record {
	if v.State == "SUCCEEDED" {
		return v
	}
	var targets []target
	if get(ctx, "/json/list", &targets) != nil {
		v.State = "UNKNOWN"
		return v
	}
	for _, t := range targets {
		if t.ID == v.Target && t.URL == v.URL {
			v.State = "SUCCEEDED"
			_ = a.save(v)
			return v
		}
	}
	v.State = "UNKNOWN"
	return v
}
func (a *Agent) open(w http.ResponseWriter, r *http.Request) {
	var b struct {
		OperationID string `json:"operationId"`
		URL         string `json:"url"`
	}
	if !protocol.Decode(w, r, &b) {
		return
	}
	if !protocol.ValidID(b.OperationID) || protocol.ValidateURL(b.URL) != nil {
		protocol.Error(w, 400, "INVALID_REQUEST")
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if v, e := a.load(b.OperationID); e == nil {
		if v.URL != b.URL {
			protocol.Error(w, 409, "IDEMPOTENCY_CONFLICT")
			return
		}
		v = a.recover(r.Context(), v)
		if v.State != "SUCCEEDED" {
			protocol.Error(w, 409, "OPEN_RESULT_UNKNOWN")
			return
		}
		protocol.JSON(w, 200, v)
		return
	} else if !os.IsNotExist(e) {
		protocol.Error(w, 500, "OPERATION_RECORD_INVALID")
		return
	}
	v := record{ID: b.OperationID, URL: b.URL, State: "PREPARED"}
	if a.save(v) != nil {
		protocol.Error(w, 500, "OPERATION_SAVE_FAILED")
		return
	}
	raw, e := browserCall(r.Context(), "Target.createTarget", map[string]string{"url": "about:blank#cb-" + v.ID})
	if e != nil {
		protocol.Error(w, 502, "OPEN_RESULT_UNKNOWN")
		return
	}
	var created struct {
		TargetID string `json:"targetId"`
	}
	if json.Unmarshal(raw, &created) != nil || created.TargetID == "" {
		protocol.Error(w, 502, "OPEN_RESULT_UNKNOWN")
		return
	}
	v.Target = created.TargetID
	if a.save(v) != nil {
		protocol.Error(w, 500, "OPERATION_SAVE_FAILED")
		return
	}
	var targets []target
	if get(r.Context(), "/json/list", &targets) != nil {
		protocol.Error(w, 502, "OPEN_RESULT_UNKNOWN")
		return
	}
	for _, t := range targets {
		if t.ID != v.Target {
			continue
		}
		_, e = call(r.Context(), t.WebSocket, "Page.navigate", map[string]string{"url": v.URL})
		if e != nil {
			protocol.Error(w, 502, "OPEN_RESULT_UNKNOWN")
			return
		}
		_, _ = browserCall(r.Context(), "Target.activateTarget", map[string]string{"targetId": v.Target})
		v.State = "SUCCEEDED"
		if a.save(v) != nil {
			protocol.Error(w, 500, "OPERATION_SAVE_FAILED")
			return
		}
		protocol.JSON(w, 200, v)
		return
	}
	protocol.Error(w, 502, "OPEN_RESULT_UNKNOWN")
}
func (a *Agent) operation(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !protocol.ValidID(id) {
		protocol.Error(w, 400, "INVALID_ID")
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	v, e := a.load(id)
	if e != nil {
		protocol.Error(w, 404, "NOT_FOUND")
		return
	}
	protocol.JSON(w, 200, a.recover(r.Context(), v))
}
func (a *Agent) audio(w http.ResponseWriter, r *http.Request) {
	c, e := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if e != nil {
		return
	}
	defer c.CloseNow()
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	go func() {
		for {
			if _, _, e := c.Read(ctx); e != nil {
				cancel()
				return
			}
		}
	}()
	cmd := exec.CommandContext(ctx, "parec", "--device=cloud.monitor", "--format=s16le", "--rate=48000", "--channels=2", "--latency-msec=20")
	pipe, e := cmd.StdoutPipe()
	if e != nil {
		return
	}
	if cmd.Start() != nil {
		return
	}
	defer func() { cancel(); _ = pipe.Close(); _ = cmd.Wait() }()
	frames := make(chan []byte, 5)
	go func() {
		defer close(frames)
		for {
			frame := make([]byte, 3840)
			if _, e := io.ReadFull(pipe, frame); e != nil {
				return
			}
			select {
			case frames <- frame:
			default:
				select {
				case <-frames:
				default:
				}
				select {
				case frames <- frame:
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	for {
		select {
		case <-ctx.Done():
			return
		case b, ok := <-frames:
			if !ok {
				return
			}
			cctx, done := context.WithTimeout(ctx, 250*time.Millisecond)
			e = c.Write(cctx, websocket.MessageBinary, b)
			done()
			if e != nil {
				return
			}
		}
	}
}
func (a *Agent) waitForFileChooser(w http.ResponseWriter, r *http.Request) {
	target, err := activeTarget(r.Context())
	if err != nil {
		protocol.Error(w, 503, "ACTIVE_TARGET_UNAVAILABLE")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
	defer cancel()
	connection, _, err := websocket.Dial(ctx, target.WebSocket, nil)
	if err != nil {
		protocol.Error(w, 503, "CDP_UNAVAILABLE")
		return
	}
	connection.SetReadLimit(4 << 20)
	if err = cdpCommand(ctx, connection, 1, "Page.enable", map[string]any{}); err != nil {
		connection.CloseNow()
		protocol.Error(w, 503, "CDP_UNAVAILABLE")
		return
	}
	command, _ := json.Marshal(map[string]any{
		"id": 2, "method": "Page.setInterceptFileChooserDialog", "params": map[string]bool{"enabled": true},
	})
	if err = connection.Write(ctx, websocket.MessageText, command); err != nil {
		connection.CloseNow()
		protocol.Error(w, 503, "FILE_CHOOSER_UNAVAILABLE")
		return
	}
	for {
		_, data, readErr := connection.Read(ctx)
		if readErr != nil {
			connection.CloseNow()
			if ctx.Err() != nil {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			protocol.Error(w, 503, "FILE_CHOOSER_UNAVAILABLE")
			return
		}
		var event struct {
			ID     int             `json:"id"`
			Error  json.RawMessage `json:"error"`
			Method string          `json:"method"`
			Params struct {
				Mode          string `json:"mode"`
				BackendNodeID int64  `json:"backendNodeId"`
			} `json:"params"`
		}
		if json.Unmarshal(data, &event) != nil {
			continue
		}
		if event.ID == 2 && len(event.Error) > 0 {
			connection.CloseNow()
			protocol.Error(w, 503, "FILE_CHOOSER_UNAVAILABLE")
			return
		}
		if event.Method != "Page.fileChooserOpened" || event.Params.BackendNodeID == 0 {
			continue
		}
		id := auth.ID()
		expires := time.Now().Add(time.Minute)
		a.chooserMu.Lock()
		if a.choosers == nil {
			a.choosers = map[string]*fileChooser{}
		}
		a.choosers[id] = &fileChooser{connection: connection, backendNodeID: event.Params.BackendNodeID, expires: expires}
		a.chooserMu.Unlock()
		time.AfterFunc(time.Minute, func() {
			a.chooserMu.Lock()
			if chooser, ok := a.choosers[id]; ok && time.Now().After(chooser.expires) {
				chooser.connection.CloseNow()
				delete(a.choosers, id)
			}
			a.chooserMu.Unlock()
		})
		protocol.JSON(w, 200, map[string]any{"id": id, "multiple": event.Params.Mode == "selectMultiple"})
		return
	}
}

func (a *Agent) completeFileChooser(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !protocol.ValidID(id) {
		protocol.Error(w, 400, "INVALID_ID")
		return
	}
	var body struct {
		Names []string `json:"names"`
	}
	if !protocol.Decode(w, r, &body) {
		return
	}
	if len(body.Names) > 20 {
		protocol.Error(w, 400, "TOO_MANY_FILES")
		return
	}
	a.chooserMu.Lock()
	chooser := a.choosers[id]
	delete(a.choosers, id)
	a.chooserMu.Unlock()
	if chooser == nil || time.Now().After(chooser.expires) {
		if chooser != nil {
			chooser.connection.CloseNow()
		}
		protocol.Error(w, 410, "FILE_CHOOSER_EXPIRED")
		return
	}
	defer chooser.connection.CloseNow()
	paths := make([]string, 0, len(body.Names))
	uploads, err := os.OpenRoot(filepath.Join(a.Home, "Uploads"))
	if err != nil {
		protocol.Error(w, 500, "STORAGE_UNAVAILABLE")
		return
	}
	defer uploads.Close()
	for _, name := range body.Names {
		if name == "" || name != filepath.Base(name) || strings.ContainsAny(name, "/\\\x00\r\n") {
			protocol.Error(w, 400, "INVALID_FILENAME")
			return
		}
		info, statErr := uploads.Lstat(name)
		if statErr != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			protocol.Error(w, 404, "FILE_NOT_FOUND")
			return
		}
		paths = append(paths, filepath.Join(a.Home, "Uploads", name))
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	err = cdpCommand(ctx, chooser.connection, 3, "DOM.setFileInputFiles", map[string]any{
		"files":         paths,
		"backendNodeId": chooser.backendNodeID,
	})
	if err != nil {
		protocol.Error(w, 409, "FILE_CHOOSER_STALE")
		return
	}
	protocol.JSON(w, 200, map[string]bool{"ok": true})
}

func (a *Agent) input(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Text   string `json:"text"`
		Key    string `json:"key"`
		Action string `json:"action"`
	}
	if !protocol.Decode(w, r, &b) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	if (b.Action == "insert" || b.Action == "paste") && b.Text == "" {
		protocol.JSON(w, 200, map[string]bool{"ok": true})
		return
	}
	if b.Action == "clipboard-read" || b.Action == "copy" || b.Action == "cut" {
		if b.Action == "copy" || b.Action == "cut" {
			key := "ctrl+c"
			if b.Action == "cut" {
				key = "ctrl+x"
			}
			if exec.CommandContext(ctx, "xdotool", "key", "--clearmodifiers", key).Run() != nil {
				protocol.Error(w, 500, "INPUT_FAILED")
				return
			}
			time.Sleep(100 * time.Millisecond)
		}
		out, e := exec.CommandContext(ctx, "xclip", "-selection", "clipboard", "-o").Output()
		if e != nil || len(out) > 16384 {
			protocol.Error(w, 400, "CLIPBOARD_UNAVAILABLE")
			return
		}
		protocol.JSON(w, 200, map[string]string{"text": string(out)})
		return
	}
	if b.Action == "insert" && b.Text != "" {
		if len(b.Text) > 16384 {
			protocol.Error(w, 400, "INPUT_TOO_LARGE")
			return
		}
		target, targetErr := activeTarget(ctx)
		if targetErr != nil {
			protocol.Error(w, 503, "ACTIVE_TARGET_UNAVAILABLE")
			return
		}
		if _, targetErr = call(ctx, target.WebSocket, "Input.insertText", map[string]string{"text": b.Text}); targetErr != nil {
			protocol.Error(w, 500, "INPUT_FAILED")
			return
		}
	} else if b.Text != "" {
		cmd := exec.CommandContext(ctx, "xclip", "-selection", "clipboard")
		cmd.Stdin = strings.NewReader(b.Text)
		if cmd.Run() != nil {
			protocol.Error(w, 500, "INPUT_FAILED")
			return
		}
		if b.Action == "paste" {
			if exec.CommandContext(ctx, "xdotool", "key", "--clearmodifiers", "ctrl+v").Run() != nil {
				protocol.Error(w, 500, "INPUT_FAILED")
				return
			}
		}
	} else {
		allowed := map[string]bool{
			"Return": true, "Escape": true, "Tab": true, "BackSpace": true, "Delete": true,
			"Left": true, "Right": true, "Up": true, "Down": true, "Home": true, "End": true,
			"Page_Up": true, "Page_Down": true, "ctrl+a": true, "ctrl+l": true, "ctrl+t": true,
			"ctrl+w": true, "ctrl+r": true, "ctrl+f": true, "ctrl+z": true, "ctrl+y": true,
			"alt+Left": true, "alt+Right": true, "F1": true, "F2": true, "F3": true, "F4": true,
			"F5": true, "F6": true, "F7": true, "F8": true, "F9": true, "F10": true, "F11": true, "F12": true,
		}
		if !allowed[b.Key] {
			protocol.Error(w, 400, "INVALID_KEY")
			return
		}
		if exec.CommandContext(ctx, "xdotool", "key", "--clearmodifiers", b.Key).Run() != nil {
			protocol.Error(w, 500, "INPUT_FAILED")
			return
		}
	}
	protocol.JSON(w, 200, map[string]bool{"ok": true})
}
func CloseBrowser(ctx context.Context) { _, _ = browserCall(ctx, "Browser.close", map[string]any{}) }
