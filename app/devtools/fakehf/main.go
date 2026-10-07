// fakehf:模拟 Hugging Face 下载,用来本地测下载/断点续传/校验,不碰真网络。
//
//	HEAD/GET /{owner}/{repo}/resolve/{rev}/{file...}
//	    → 302 到 /cdn/{file},带 X-Linked-Size / X-Linked-Etag(sha256),和 HF 一样
//	GET /cdn/{file}  支持 Range,按 -rate 限速
//
// 每个文件名对应一段确定的伪随机内容(开头是 GGUF 魔数),大小 -size MiB。
// 文件名里带 "missing" 的返回 404。-drop-at N 让第一次下载在第 N MiB 处断线。
// -changed 列出的文件换一份内容(模拟 HF 上的模型更新了,用来测「检查更新」)。
package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"hash/crc32"
	"log"
	"math/rand"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type blob struct {
	data []byte
	sha  string
}

var (
	mu    sync.Mutex
	blobs = map[string]*blob{}
	size  = flag.Int("size", 48, "每个假模型文件的大小(MiB)")
	rate  = flag.Float64("rate", 8, "限速 MiB/s,0=不限")
	drop  = flag.Int("drop-at", 0, "第一次 GET 传到这么多 MiB 时断线,0=不断")
	addr  = flag.String("addr", "127.0.0.1:9180", "监听地址")
	chg   = flag.String("changed", "", "逗号分隔的文件名:这些文件用另一份内容(模拟模型更新)")
	drops atomic.Int32
)

func get(file string) *blob {
	mu.Lock()
	defer mu.Unlock()
	if b, ok := blobs[file]; ok {
		return b
	}
	d := make([]byte, *size<<20)
	seed := int64(crc32.ChecksumIEEE([]byte(file)))
	for _, c := range strings.Split(*chg, ",") {
		if strings.TrimSpace(c) == file {
			seed += 7919
		}
	}
	rand.New(rand.NewSource(seed)).Read(d)
	copy(d, "GGUF")
	s := sha256.Sum256(d)
	b := &blob{d, hex.EncodeToString(s[:])}
	blobs[file] = b
	return b
}

type throttled struct {
	http.ResponseWriter
	sent    int64
	dropAt  int64
	start   time.Time
	dropped bool
}

func (t *throttled) Write(p []byte) (int, error) {
	n := 0
	for len(p) > 0 {
		c := min(len(p), 64<<10)
		if t.dropAt > 0 && t.sent+int64(c) > t.dropAt {
			t.dropped = true
			return n, fmt.Errorf("simulated drop")
		}
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
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		if i := strings.Index(path, "/resolve/"); i > 0 && (r.Method == "GET" || r.Method == "HEAD") {
			rest := path[i+len("/resolve/"):]
			j := strings.Index(rest, "/")
			if j < 0 {
				http.NotFound(w, r)
				return
			}
			file := rest[j+1:]
			log.Printf("%s %s", r.Method, path)
			if strings.Contains(file, "missing") {
				http.Error(w, "Entry not found", 404)
				return
			}
			b := get(file)
			w.Header().Set("X-Linked-Size", strconv.Itoa(len(b.data)))
			w.Header().Set("X-Linked-Etag", `"`+b.sha+`"`)
			w.Header().Set("X-Repo-Commit", "fakecommit")
			http.Redirect(w, r, "/cdn/"+file+"?X-Amz-Signature=fake", http.StatusFound)
			return
		}
		if strings.HasPrefix(path, "/cdn/") {
			file := strings.TrimPrefix(path, "/cdn/")
			b := get(file)
			log.Printf("%s /cdn/%s Range=%q", r.Method, file, r.Header.Get("Range"))
			tw := &throttled{ResponseWriter: w, start: time.Now()}
			if *drop > 0 && r.Method == "GET" && drops.Add(1) == 1 {
				tw.dropAt = int64(*drop) << 20
			}
			http.ServeContent(tw, r, file, time.Time{}, bytes.NewReader(b.data))
			if tw.dropped {
				log.Printf("  模拟断线 @ %d 字节", tw.sent)
				if hj, ok := w.(http.Hijacker); ok {
					if c, _, err := hj.Hijack(); err == nil {
						c.Close()
					}
				}
			}
			return
		}
		http.NotFound(w, r)
	})
	log.Printf("fakehf 监听 %s,文件 %d MiB,限速 %.1f MiB/s", *addr, *size, *rate)
	log.Fatal(http.ListenAndServe(*addr, nil))
}
