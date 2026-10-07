// fakellama:极小的假 llama-server,只用于本地测试启动器和网页,不加载任何模型。
//
// 接受 llama-server 的命令行(只认 -m/--host/--port,其余忽略),模拟:
//   - GET  /health      加载期间 503,FAKE_LOAD_MS 毫秒后 200
//   - GET  /props
//   - POST /tokenize    粗略估 token 数
//   - POST /completion  校验提示词格式(和 promptfmt.py 一字不差),按 SSE 流式吐字
//
// 输出:草稿命中 FAKE_FIXTURES(devtools/fixtures/samples.json)时吐模型真实输出,
// 否则吐一个简单变换后的文本。
// FAKE_LOCKS(测 Create fact):drop = 每次都删掉 [[…_LOCK_n]] 占位符;flaky = 第 1、3、5… 次删,其余照常。
//
// 二进制名里带 "fail" → 启动即崩;带 "nogpu" → 报告 0 层上 GPU(测试后端回退)。
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/sgaofen/humanizer/app/internal/launcher"
)

var (
	prefix, suffix string
	fixtures       = map[string]string{}
	loaded         atomic.Bool
	tokenDelay     = 14 * time.Millisecond
	lockRe         = regexp.MustCompile(`\[\[_*HZ_LOCK_\d+\]\]`)
	lockCalls      atomic.Int64
)

func main() {
	host, port, model := "127.0.0.1", "8080", ""
	args := os.Args[1:]
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--host":
			i++
			host = args[i]
		case "--port":
			i++
			port = args[i]
		case "-m", "--model":
			i++
			model = args[i]
		}
	}
	name := filepath.Base(os.Args[0])
	logf := func(f string, a ...any) { fmt.Fprintf(os.Stderr, f+"\n", a...) }
	logf("build: 11335 (fake0000) with fake compiler for test")
	if strings.Contains(name, "fail") {
		time.Sleep(300 * time.Millisecond)
		logf("ggml_backend_load: failed to load backend (fake failure)")
		os.Exit(1)
	}
	if model != "" {
		if _, err := os.Stat(model); err != nil {
			logf("llama_model_load: error loading model: %v", err)
			os.Exit(1)
		}
	}
	p := launcher.BuildPrompt("\x00DRAFT\x00")
	i := strings.Index(p, "\x00DRAFT\x00")
	prefix, suffix = p[:i], p[i+len("\x00DRAFT\x00"):]

	if fp := os.Getenv("FAKE_FIXTURES"); fp != "" {
		var f struct {
			Samples []struct{ Draft, Output string } `json:"samples"`
		}
		if b, err := os.ReadFile(fp); err == nil && json.Unmarshal(b, &f) == nil {
			for _, s := range f.Samples {
				fixtures[strings.TrimSpace(s.Draft)] = s.Output
			}
		}
		logf("fake: %d fixtures", len(fixtures))
	}
	if v, err := strconv.Atoi(os.Getenv("FAKE_TOKEN_MS")); err == nil {
		tokenDelay = time.Duration(v) * time.Millisecond
	}
	loadMs := 1200
	if v, err := strconv.Atoi(os.Getenv("FAKE_LOAD_MS")); err == nil {
		loadMs = v
	}
	gpu := 49
	if strings.Contains(name, "nogpu") {
		gpu = 0
	}
	logf("llama_model_load_from_file_impl: using device Metal (Fake GPU) - 16384 MiB free")
	go func() {
		time.Sleep(time.Duration(loadMs) * time.Millisecond)
		logf("load_tensors: offloaded %d/49 layers to GPU", gpu)
		loaded.Store(true)
		logf("main: model loaded")
	}()

	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		if !loaded.Load() {
			w.WriteHeader(503)
			w.Write([]byte(`{"error":{"code":503,"message":"Loading model","type":"unavailable_error"}}`))
			return
		}
		w.Write([]byte(`{"status":"ok"}`))
	})
	mux.HandleFunc("GET /props", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"model_path": model, "build_info": "b11335-fake", "total_slots": 1,
			"default_generation_settings": map[string]any{"n_ctx": 8192}, "is_sleeping": false})
	})
	mux.HandleFunc("POST /tokenize", func(w http.ResponseWriter, r *http.Request) {
		var req struct{ Content string }
		json.NewDecoder(r.Body).Decode(&req)
		n := approxTokens(req.Content)
		toks := make([]int, n)
		for i := range toks {
			toks[i] = i + 2
		}
		json.NewEncoder(w).Encode(map[string]any{"tokens": toks})
	})
	mux.HandleFunc("POST /completion", completion)
	// 和真 llama-server 一样:设了 LLAMA_API_KEY 就校验(/health 例外)
	var handler http.Handler = mux
	if key := os.Getenv("LLAMA_API_KEY"); key != "" {
		handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/health" && r.Header.Get("Authorization") != "Bearer "+key {
				w.WriteHeader(401)
				w.Write([]byte(`{"error":{"code":401,"message":"Invalid API Key"}}`))
				return
			}
			mux.ServeHTTP(w, r)
		})
	}
	addr := host + ":" + port
	logf("main: server is listening on http://%s", addr)
	if err := http.ListenAndServe(addr, handler); err != nil {
		logf("couldn't bind HTTP server socket: %v", err)
		os.Exit(1)
	}
}

func approxTokens(s string) int {
	n := 0
	inWord := false
	for _, r := range s {
		switch {
		case unicode.Is(unicode.Han, r):
			n++
			inWord = false
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			if !inWord {
				n++
			}
			inWord = true
		case unicode.IsSpace(r):
			inWord = false
		default:
			n++
			inWord = false
		}
	}
	return n * 13 / 10
}

func completion(w http.ResponseWriter, r *http.Request) {
	var req map[string]any
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":{"message":"bad json"}}`, 400)
		return
	}
	prompt, _ := req["prompt"].(string)
	sum := sha256.Sum256([]byte(prompt))
	params := map[string]any{}
	for k, v := range req {
		if k != "prompt" {
			params[k] = v
		}
	}
	pj, _ := json.Marshal(params)
	fmt.Fprintf(os.Stderr, "REQUEST prompt_sha=%s params=%s\n", hex.EncodeToString(sum[:])[:16], pj)

	if !strings.HasPrefix(prompt, prefix) || !strings.HasSuffix(prompt, suffix) || len(prompt) < len(prefix)+len(suffix) {
		w.WriteHeader(400)
		w.Write([]byte(`{"error":{"code":400,"message":"fake: prompt format mismatch (instr/sep not byte-identical)"}}`))
		return
	}
	draft := prompt[len(prefix) : len(prompt)-len(suffix)]
	out, ok := fixtures[strings.TrimSpace(draft)]
	if !ok {
		out = synth(draft)
	}
	if lockRe.MatchString(draft) {
		n := lockCalls.Add(1)
		if m := os.Getenv("FAKE_LOCKS"); m == "drop" || (m == "flaky" && n%2 == 1) {
			out = lockRe.ReplaceAllString(out, "")
		}
	}
	nPredict := 2048
	if v, ok := req["n_predict"].(float64); ok && v > 0 {
		nPredict = int(v)
	}
	pieces := split(out)
	stopType := "eos"
	if len(pieces) > nPredict {
		pieces, stopType = pieces[:nPredict], "limit"
	}
	stream, _ := req["stream"].(bool)
	if !stream {
		json.NewEncoder(w).Encode(map[string]any{"content": strings.Join(pieces, ""), "stop": true, "stop_type": stopType,
			"tokens_predicted": len(pieces)})
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	fl, _ := w.(http.Flusher)
	send := func(v any) bool {
		b, _ := json.Marshal(v)
		if _, err := fmt.Fprintf(w, "data: %s\n\n", b); err != nil {
			return false
		}
		if fl != nil {
			fl.Flush()
		}
		return true
	}
	nPrompt := approxTokens(prompt)
	if rp, _ := req["return_progress"].(bool); rp {
		send(map[string]any{"content": "", "stop": false, "prompt_progress": map[string]any{"total": nPrompt, "cache": 0, "processed": nPrompt / 2, "time_ms": 20}})
		time.Sleep(60 * time.Millisecond)
		send(map[string]any{"content": "", "stop": false, "prompt_progress": map[string]any{"total": nPrompt, "cache": 0, "processed": nPrompt, "time_ms": 45}})
	}
	fmt.Fprint(w, ": ping\n\n") // llama-server 会发 SSE 注释保活,网页要能跳过
	start := time.Now()
	for i, p := range pieces {
		select {
		case <-r.Context().Done():
			fmt.Fprintln(os.Stderr, "fake: client disconnected, stop generating")
			return
		default:
		}
		if !send(map[string]any{"content": p, "stop": false, "tokens": []int{i}}) {
			return
		}
		time.Sleep(tokenDelay)
	}
	el := time.Since(start).Seconds()
	send(map[string]any{"content": "", "stop": true, "stop_type": stopType, "tokens_predicted": len(pieces),
		"tokens_evaluated": nPrompt, "truncated": false,
		"timings": map[string]any{"predicted_per_second": float64(len(pieces)) / max(el, 0.001), "prompt_per_second": 850.0}})
}

// split 把文本切成类似 token 的小块:英文按词(连同后面的空白),中文一到两个字。
func split(s string) []string {
	var out []string
	var cur strings.Builder
	flush := func() {
		if cur.Len() > 0 {
			out = append(out, cur.String())
			cur.Reset()
		}
	}
	han := 0
	for len(s) > 0 {
		r, n := utf8.DecodeRuneInString(s)
		switch {
		case unicode.Is(unicode.Han, r):
			if han == 0 || han >= 2 {
				flush()
				han = 0
			}
			cur.WriteRune(r)
			han++
		case unicode.IsSpace(r):
			cur.WriteRune(r)
			if r == '\n' {
				flush()
			}
			han = 0
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			if han > 0 || (cur.Len() > 0 && strings.ContainsAny(cur.String()[cur.Len()-1:], " \t")) {
				flush()
			}
			han = 0
			cur.WriteRune(r)
		default:
			flush()
			cur.WriteRune(r)
			flush()
			han = 0
		}
		s = s[n:]
	}
	flush()
	return out
}

// synth:没有夹具时的假改写 —— 每段内句子倒序,够看出流式和差异高亮就行。
func synth(draft string) string {
	paras := strings.Split(strings.TrimSpace(draft), "\n\n")
	for i, p := range paras {
		var sents []string
		var cur strings.Builder
		for _, r := range p {
			cur.WriteRune(r)
			if strings.ContainsRune(".!?。!?", r) {
				sents = append(sents, strings.TrimSpace(cur.String()))
				cur.Reset()
			}
		}
		if t := strings.TrimSpace(cur.String()); t != "" {
			sents = append(sents, t)
		}
		for l, rr := 0, len(sents)-1; l < rr; l, rr = l+1, rr-1 {
			sents[l], sents[rr] = sents[rr], sents[l]
		}
		sep := " "
		if strings.ContainsFunc(p, func(r rune) bool { return unicode.Is(unicode.Han, r) }) {
			sep = ""
		}
		paras[i] = strings.Join(sents, sep)
	}
	return "(fake) " + strings.Join(paras, "\n\n")
}
