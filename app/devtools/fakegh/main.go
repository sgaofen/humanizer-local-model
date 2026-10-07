// fakegh:模拟 GitHub Releases,用来本地测「检查更新」(App 安装包),不碰真网络。
//
//	GET /repos/{owner}/{repo}/releases     发布列表(和 GitHub API 同样的字段,资产带 sha256 digest)
//	GET /dl/{tag}/{name}                   302 → /blob/{name}(和 GitHub 一样跳到另一个地址)
//	GET /blob/{name}                       支持 Range,按 -rate 限速
//
// -dir 里的 Humanizer-<版本>-*.{dmg,exe,zip} 按版本分组成一个个发布;每次请求都重新扫描目录,
// 往目录里放新文件就等于「发布了新版本」。-notes 是说明模板({v} 换成版本号),不给就用内置的中英文说明。
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

var (
	addr  = flag.String("addr", "127.0.0.1:9290", "监听地址")
	dir   = flag.String("dir", ".", "放安装包的目录")
	notes = flag.String("notes", "", "发布说明模板文件({v} 换成版本号)")
	rate  = flag.Float64("rate", 0, "下载限速 MiB/s,0=不限")
	repo  = flag.String("repo", "sgaofen/humanizer-local-model", "仓库名(只影响 html_url)")
)

const defaultNotes = "**humanizer** rewrites AI-written drafts so they read like a person wrote them, keeping every number, name, date and quote. 12B model, runs entirely on your computer. Apache 2.0.\n\n" +
	"## What changed in {v}\n\n" +
	"- **Check for updates.** The **⋯** menu has a new **Check for updates** item. The app looks once a day (you can turn that off) and shows a small hint when a new version or a newer model file is out. Updating keeps your model, settings and history.\n" +
	"- **Model updates.** When a model file on Hugging Face changes, you can download the new one while you keep rewriting; it is swapped in after it passes its checksum.\n" +
	"- **Fixed:** a rare case where the size picker showed the wrong download size for `Q3`.\n\n" +
	"## Download\n\n| Your computer | File |\n|---|---|\n| Mac with Apple silicon (M1 or newer) | `Humanizer-{v}-macos-arm64.dmg` |\n| Windows x64 | `Humanizer-{v}-windows-x64-setup.exe` (installer) or `Humanizer-{v}-windows-x64-portable.zip` |\n\n" +
	"Checksums: `SHA256SUMS.txt`.\n\n---\n\n" +
	"**humanizer** 把 AI 写的草稿改写成读起来像人写的，数字、人名、日期、引语一个不丢。\n\n" +
	"**{v} 改了什么**\n" +
	"- **检查更新**：右上角「⋯」菜单里新增「检查更新」。App 每天自动看一次（可以关掉），有新版本或 Hugging Face 上的模型文件更新了，会给一个不打扰的小提示。更新后模型、设置和历史记录都保留。\n" +
	"- **模型更新**：模型文件更新后，可以一边照常改写一边下载新版，校验通过后再替换。\n" +
	"- **修复**：选档页偶尔把 `Q3` 的下载大小显示错。\n\n" +
	"**下载**：Mac（Apple 芯片）用 `.dmg`；Windows 用 `setup.exe` 安装包或免安装 `portable.zip`。\n"

var pkgRe = regexp.MustCompile(`^Humanizer-(\d+\.\d+\.\d+)-.+\.(dmg|exe|zip)$`)

type asset struct {
	Name   string `json:"name"`
	Size   int64  `json:"size"`
	URL    string `json:"browser_download_url"`
	Digest string `json:"digest"`
	State  string `json:"state"`
}

var (
	hashMu sync.Mutex
	hashes = map[string]string{} // 路径|大小|修改时间 → sha256
)

func fileSHA(p string, st os.FileInfo) string {
	key := fmt.Sprintf("%s|%d|%d", p, st.Size(), st.ModTime().UnixNano())
	hashMu.Lock()
	defer hashMu.Unlock()
	if h, ok := hashes[key]; ok {
		return h
	}
	f, err := os.Open(p)
	if err != nil {
		return ""
	}
	defer f.Close()
	h := sha256.New()
	io.Copy(h, f)
	s := hex.EncodeToString(h.Sum(nil))
	hashes[key] = s
	return s
}

func releases(base string) []map[string]any {
	ents, _ := os.ReadDir(*dir)
	byVer := map[string][]asset{}
	for _, e := range ents {
		m := pkgRe.FindStringSubmatch(e.Name())
		if m == nil {
			continue
		}
		p := filepath.Join(*dir, e.Name())
		st, err := os.Stat(p)
		if err != nil {
			continue
		}
		v := m[1]
		byVer[v] = append(byVer[v], asset{Name: e.Name(), Size: st.Size(), State: "uploaded",
			URL: base + "/dl/app-v" + v + "/" + e.Name(), Digest: "sha256:" + fileSHA(p, st)})
	}
	tmpl := defaultNotes
	if *notes != "" {
		if b, err := os.ReadFile(*notes); err == nil {
			tmpl = string(b)
		}
	}
	var vs []string
	for v := range byVer {
		vs = append(vs, v)
	}
	sort.Sort(sort.Reverse(sort.StringSlice(vs)))
	var out []map[string]any
	for _, v := range vs {
		out = append(out, map[string]any{
			"tag_name": "app-v" + v, "name": "humanizer " + v + ": check for updates (macOS · Windows)",
			"body": strings.ReplaceAll(tmpl, "{v}", v), "draft": false, "prerelease": false,
			"html_url":     "https://github.com/" + *repo + "/releases/tag/app-v" + v,
			"published_at": time.Now().UTC().Format(time.RFC3339), "assets": byVer[v],
		})
	}
	return out
}

type throttled struct {
	http.ResponseWriter
	sent  int64
	start time.Time
}

func (t *throttled) Write(p []byte) (int, error) {
	n := 0
	for len(p) > 0 {
		c := min(len(p), 64<<10)
		w, err := t.ResponseWriter.Write(p[:c])
		n += w
		t.sent += int64(w)
		if err != nil {
			return n, err
		}
		if f, ok := t.ResponseWriter.(http.Flusher); ok {
			f.Flush()
		}
		if *rate > 0 {
			want := time.Duration(float64(t.sent) / (*rate * (1 << 20)) * float64(time.Second))
			if el := time.Since(t.start); el < want {
				time.Sleep(want - el)
			}
		}
		p = p[c:]
	}
	return n, nil
}

func main() {
	flag.Parse()
	base := "http://" + *addr
	http.HandleFunc("/repos/", func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/releases") {
			http.NotFound(w, r)
			return
		}
		log.Printf("%s %s UA=%q", r.Method, r.URL.Path, r.UserAgent())
		b, _ := json.Marshal(releases(base))
		w.Header().Set("Content-Type", "application/json")
		w.Write(b)
	})
	http.HandleFunc("/dl/", func(w http.ResponseWriter, r *http.Request) {
		log.Printf("%s %s", r.Method, r.URL.Path)
		http.Redirect(w, r, "/blob/"+filepath.Base(r.URL.Path)+"?X-Amz-Signature=fake", http.StatusFound)
	})
	http.HandleFunc("/blob/", func(w http.ResponseWriter, r *http.Request) {
		name := filepath.Base(r.URL.Path)
		if !pkgRe.MatchString(name) {
			http.NotFound(w, r)
			return
		}
		f, err := os.Open(filepath.Join(*dir, name))
		if err != nil {
			http.NotFound(w, r)
			return
		}
		defer f.Close()
		st, _ := f.Stat()
		log.Printf("%s /blob/%s Range=%q", r.Method, name, r.Header.Get("Range"))
		http.ServeContent(&throttled{ResponseWriter: w, start: time.Now()}, r, name, st.ModTime(), f)
	})
	log.Printf("fakegh 监听 %s,目录 %s", *addr, *dir)
	log.Fatal(http.ListenAndServe(*addr, nil))
}
