package launcher

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

// Download 是一个可断点续传的单文件下载。
//
//   - 先 HEAD 拿大小和 sha256(HF 在 302 上返回 X-Linked-Size / X-Linked-Etag);
//   - 写到 <dest>.part,旁边放 <dest>.part.json 记住远端大小,下次从断点 Range 续;
//   - 网络抖动自动重试(指数退避),60 秒没收到字节视为卡死,断开重连;
//   - 下完先查 GGUF 魔数(防止镜像/门户页返回 HTML),再校验 sha256,最后改名。
type Download struct {
	URL          string
	Dest         string
	SHA256       string // 期望值;空则用 HF 返回的值;都没有就不校验
	Client       *http.Client
	Logf         func(format string, args ...any)
	StallTimeout time.Duration
	MaxRetries   int
	UserAgent    string                  // 空 = Go 默认;检查更新/下载更新用 "humanizer-app/<版本>"
	ExpectSize   int64                   // >0 时远端和下完的文件都必须正好这么大(GitHub Release 会给)
	Verify       func(path string) error // 下完后的内容检查;nil = 查 GGUF 魔数

	// SHA 是下完后实际校验通过的 sha256(没校验就是空),用来记下本地模型的指纹。
	SHA string

	Received   atomic.Int64
	Total      atomic.Int64
	VerifyDone atomic.Int64
	Attempt    atomic.Int32
	phase      atomic.Value // "probe" | "fetch" | "verify"

	mu      sync.Mutex
	samples []dlSample
}

type dlSample struct {
	t time.Time
	n int64
}

type remoteMeta struct {
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256,omitempty"`
	URL    string `json:"url"`
}

// PermanentError 表示重试也没用(404、无权限、磁盘满、校验失败……)。
type PermanentError struct {
	Code string // not_found / forbidden / disk_full / checksum / not_gguf
	Msg  string
}

func (e *PermanentError) Error() string { return e.Msg }

var sha256Re = regexp.MustCompile(`^[0-9a-f]{64}$`)

func newHTTPClient() *http.Client {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.DisableCompression = true // 一定要拿原始字节,否则长度和 Range 都对不上
	tr.ResponseHeaderTimeout = 30 * time.Second
	return &http.Client{Transport: tr}
}

func (d *Download) Phase() string {
	if v, ok := d.phase.Load().(string); ok {
		return v
	}
	return ""
}

func (d *Download) logf(f string, a ...any) {
	if d.Logf != nil {
		d.Logf(f, a...)
	}
}

// Speed 返回最近几秒的平均速度(字节/秒)。
func (d *Download) Speed() float64 {
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.samples) < 2 {
		return 0
	}
	a, b := d.samples[0], d.samples[len(d.samples)-1]
	if time.Since(b.t) > 5*time.Second { // 好一阵没数据了
		return 0
	}
	dt := b.t.Sub(a.t).Seconds()
	if dt <= 0 {
		return 0
	}
	return float64(b.n-a.n) / dt
}

func (d *Download) sample(n int64) {
	now := time.Now()
	d.mu.Lock()
	defer d.mu.Unlock()
	if k := len(d.samples); k > 0 && now.Sub(d.samples[k-1].t) < 200*time.Millisecond {
		return
	}
	d.samples = append(d.samples, dlSample{now, n})
	cut := 0
	for cut < len(d.samples)-2 && now.Sub(d.samples[cut].t) > 6*time.Second {
		cut++
	}
	d.samples = d.samples[cut:]
}

func (d *Download) resetSamples() {
	d.mu.Lock()
	d.samples = nil
	d.mu.Unlock()
}

func (d *Download) Run(ctx context.Context) error {
	if d.Client == nil {
		d.Client = newHTTPClient()
	}
	if d.StallTimeout == 0 {
		d.StallTimeout = 60 * time.Second
	}
	if d.MaxRetries == 0 {
		d.MaxRetries = 12
	}
	part := d.Dest + ".part"
	side := part + ".json"

	d.phase.Store("probe")
	var meta remoteMeta
	var err error
	for i := 1; ; i++ {
		meta, err = d.probe(ctx)
		if err == nil {
			break
		}
		var pe *PermanentError
		if errors.As(err, &pe) || ctx.Err() != nil || i >= 4 {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return err
		}
		if !sleepCtx(ctx, time.Duration(i)*2*time.Second) {
			return ctx.Err()
		}
	}
	if d.ExpectSize > 0 {
		if meta.Size > 0 && meta.Size != d.ExpectSize {
			return &PermanentError{Code: "size_mismatch", Msg: fmt.Sprintf("下载源上的文件大小不对(%d 字节,应为 %d)", meta.Size, d.ExpectSize)}
		}
		meta.Size = d.ExpectSize
	}
	d.Total.Store(meta.Size)
	expect := strings.ToLower(strings.TrimSpace(d.SHA256))
	if expect == "" {
		expect = meta.SHA256
	}

	// 远端文件变了(大小或哈希不同),旧的半截文件作废
	if b, err := os.ReadFile(side); err == nil {
		var old remoteMeta
		if json.Unmarshal(b, &old) == nil && (old.Size != meta.Size || (old.SHA256 != "" && meta.SHA256 != "" && old.SHA256 != meta.SHA256)) {
			d.logf("远端文件已变化(旧 %d 字节,新 %d 字节),丢弃半截文件重下", old.Size, meta.Size)
			_ = os.Remove(part)
		}
	}
	if b, err := json.Marshal(meta); err == nil {
		_ = writeFileAtomic(side, b)
	}

	var have int64
	if st, err := os.Stat(part); err == nil {
		have = st.Size()
	}
	d.Received.Store(have)
	if meta.Size > 0 {
		if free, ok := freeDiskBytes(dirOf(part)); ok {
			need := uint64(meta.Size-have) + 256<<20
			if free < need {
				return &PermanentError{Code: "disk_full", Msg: fmt.Sprintf("磁盘空间不够:还需要 %.1f GB,只剩 %.1f GB", float64(need)/gib, float64(free)/gib)}
			}
		}
	}

	d.phase.Store("fetch")
	for attempt := 1; ; attempt++ {
		d.Attempt.Store(int32(attempt))
		err := d.fetchOnce(ctx, part, meta.Size)
		if err == nil {
			break
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		var pe *PermanentError
		if errors.As(err, &pe) || attempt >= d.MaxRetries {
			return err
		}
		wait := time.Duration(1<<min(attempt, 5)) * time.Second
		d.logf("下载中断(%v),%s 后从 %d 字节处续传(第 %d 次)", err, wait, d.Received.Load(), attempt+1)
		if !sleepCtx(ctx, wait) {
			return ctx.Err()
		}
	}

	if d.ExpectSize > 0 {
		if st, err := os.Stat(part); err != nil || st.Size() != d.ExpectSize {
			_ = os.Remove(part)
			_ = os.Remove(side)
			return &PermanentError{Code: "size_mismatch", Msg: "下完的文件大小不对,已删除,请重新下载"}
		}
	}
	check := d.Verify
	if check == nil {
		check = checkGGUFMagic
	}
	if err := check(part); err != nil {
		_ = os.Remove(part)
		_ = os.Remove(side)
		return err
	}
	if expect != "" {
		d.phase.Store("verify")
		got, err := d.hashFile(ctx, part)
		if err != nil {
			return err
		}
		if got != expect {
			_ = os.Remove(part)
			_ = os.Remove(side)
			return &PermanentError{Code: "checksum", Msg: fmt.Sprintf("文件校验失败(sha256 %s… ≠ 期望 %s…),已删除,请重新下载", got[:12], expect[:12])}
		}
		d.logf("sha256 校验通过 %s", got)
		d.SHA = got
	} else {
		d.logf("远端没给 sha256,跳过校验")
	}
	if err := os.Rename(part, d.Dest); err != nil {
		return err
	}
	_ = os.Remove(side)
	return nil
}

func (d *Download) probe(ctx context.Context) (remoteMeta, error) {
	meta := remoteMeta{URL: d.URL, Size: -1}
	noRedirect := *d.Client
	noRedirect.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, d.URL, nil)
	if err != nil {
		return meta, &PermanentError{Code: "bad_url", Msg: err.Error()}
	}
	d.setUA(req)
	resp, err := noRedirect.Do(req)
	if err != nil {
		return meta, err
	}
	resp.Body.Close()
	if pe := statusError(resp.StatusCode, d.URL); pe != nil {
		return meta, pe
	}
	if v := resp.Header.Get("X-Linked-Size"); v != "" {
		meta.Size, _ = strconv.ParseInt(v, 10, 64)
	}
	for _, h := range []string{"X-Linked-Etag", "ETag"} {
		v := strings.ToLower(strings.Trim(strings.TrimPrefix(resp.Header.Get(h), "W/"), `"`))
		if sha256Re.MatchString(v) {
			meta.SHA256 = v
			break
		}
	}
	switch {
	case resp.StatusCode >= 300 && resp.StatusCode < 400:
		if meta.Size <= 0 { // 跟着跳转再 HEAD 一次拿长度
			loc, err := resp.Location()
			if err == nil {
				if r2, err := http.NewRequestWithContext(ctx, http.MethodHead, loc.String(), nil); err == nil {
					d.setUA(r2)
					if resp2, err := d.Client.Do(r2); err == nil {
						resp2.Body.Close()
						if resp2.StatusCode == 200 && resp2.ContentLength > 0 {
							meta.Size = resp2.ContentLength
						}
					}
				}
			}
		}
	case resp.StatusCode == 200:
		if meta.Size <= 0 && resp.ContentLength > 0 {
			meta.Size = resp.ContentLength
		}
	default:
		return meta, fmt.Errorf("HEAD %s: HTTP %d", d.URL, resp.StatusCode)
	}
	return meta, nil
}

func (d *Download) setUA(r *http.Request) {
	if d.UserAgent != "" {
		r.Header.Set("User-Agent", d.UserAgent)
	}
}

func statusError(code int, u string) *PermanentError {
	switch code {
	case 404:
		return &PermanentError{Code: "not_found", Msg: "下载源上找不到这个模型文件(仓库里可能还没上传):" + redactURL(u)}
	case 401, 403:
		return &PermanentError{Code: "forbidden", Msg: "下载源拒绝访问(仓库私有、需要登录,或镜像不可用):" + redactURL(u)}
	}
	return nil
}

func redactURL(u string) string {
	p, err := url.Parse(u)
	if err != nil {
		return u
	}
	p.RawQuery = ""
	return p.String()
}

var contentRangeRe = regexp.MustCompile(`^bytes (\d+)-(\d+)/(\d+|\*)$`)

func (d *Download) fetchOnce(parent context.Context, part string, size int64) error {
	f, err := os.OpenFile(part, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return &PermanentError{Code: "io", Msg: err.Error()}
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return err
	}
	off := st.Size()
	if size > 0 && off > size {
		_ = f.Truncate(0)
		off = 0
	}
	d.Received.Store(off)
	if size > 0 && off == size {
		return nil
	}

	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, d.URL, nil)
	if err != nil {
		return &PermanentError{Code: "bad_url", Msg: err.Error()}
	}
	if off > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", off))
	}
	d.setUA(req)
	resp, err := d.Client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusPartialContent:
		m := contentRangeRe.FindStringSubmatch(resp.Header.Get("Content-Range"))
		if m == nil {
			_ = f.Truncate(0)
			return fmt.Errorf("Content-Range 格式不对: %q", resp.Header.Get("Content-Range"))
		}
		if start, _ := strconv.ParseInt(m[1], 10, 64); start != off {
			_ = f.Truncate(0)
			return fmt.Errorf("服务器从 %d 续传,本地是 %d,从头来", start, off)
		}
		if size <= 0 && m[3] != "*" {
			size, _ = strconv.ParseInt(m[3], 10, 64)
			d.Total.Store(size)
		}
		if off > 0 {
			d.logf("从 %d 字节处续传", off)
		}
	case http.StatusOK:
		if off > 0 {
			d.logf("服务器不支持断点续传,从头下载")
			if err := f.Truncate(0); err != nil {
				return err
			}
			off = 0
			d.Received.Store(0)
		}
		if size <= 0 && resp.ContentLength > 0 {
			size = resp.ContentLength
			d.Total.Store(size)
		}
	case http.StatusRequestedRangeNotSatisfiable:
		if size > 0 && off >= size {
			return nil
		}
		_ = f.Truncate(0)
		return errors.New("HTTP 416,从头下载")
	default:
		if pe := statusError(resp.StatusCode, d.URL); pe != nil {
			return pe
		}
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	if _, err := f.Seek(off, io.SeekStart); err != nil {
		return err
	}

	// 卡死检测:长时间一个字节都没收到就断开重连(重连会自动续传)
	var lastByte atomic.Int64
	lastByte.Store(time.Now().UnixNano())
	stalled := atomic.Bool{}
	go func() {
		t := time.NewTicker(2 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				if time.Since(time.Unix(0, lastByte.Load())) > d.StallTimeout {
					stalled.Store(true)
					cancel()
					return
				}
			}
		}
	}()

	w := bufio.NewWriterSize(f, 4<<20)
	buf := make([]byte, 256<<10)
	for {
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			if _, werr := w.Write(buf[:n]); werr != nil {
				return diskErr(werr)
			}
			off += int64(n)
			d.Received.Store(off)
			d.sample(off)
			lastByte.Store(time.Now().UnixNano())
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			if ferr := w.Flush(); ferr != nil {
				return diskErr(ferr)
			}
			if parent.Err() != nil {
				return parent.Err()
			}
			if stalled.Load() {
				return fmt.Errorf("%s 内没有收到数据", d.StallTimeout)
			}
			return rerr
		}
	}
	if err := w.Flush(); err != nil {
		return diskErr(err)
	}
	_ = f.Sync()
	if size > 0 && off != size {
		return fmt.Errorf("连接提前结束(%d/%d 字节)", off, size)
	}
	return nil
}

func diskErr(err error) error {
	var errno syscall.Errno
	if errors.As(err, &errno) && (errno == syscall.ENOSPC || errno == 112 /* ERROR_DISK_FULL */) {
		return &PermanentError{Code: "disk_full", Msg: "磁盘写满了,请清理空间后点继续"}
	}
	return &PermanentError{Code: "io", Msg: "写文件失败:" + err.Error()}
}

func (d *Download) hashFile(ctx context.Context, path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	buf := make([]byte, 4<<20)
	var done int64
	d.VerifyDone.Store(0)
	for {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		n, err := f.Read(buf)
		if n > 0 {
			h.Write(buf[:n])
			done += int64(n)
			d.VerifyDone.Store(done)
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", err
		}
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func checkGGUFMagic(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	var magic [4]byte
	if _, err := io.ReadFull(f, magic[:]); err != nil || string(magic[:]) != "GGUF" {
		return &PermanentError{Code: "not_gguf", Msg: "下载到的不是 GGUF 模型文件(可能被网关/镜像替换成了网页),已删除"}
	}
	return nil
}

func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

func dirOf(p string) string {
	i := strings.LastIndexAny(p, `/\`)
	if i < 0 {
		return "."
	}
	return p[:i]
}
