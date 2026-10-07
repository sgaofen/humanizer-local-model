package launcher

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"time"
)

// modelJob:一个档位的模型更新(下载到 <模型>.update → 校验 → 替换)。
// 下载期间旧模型照常可用;替换时如果引擎正在用它,先停引擎、换文件、再起,起不来就换回旧文件。
type modelJob struct {
	Tier      string
	State     string // downloading / paused / waiting / installing / done / error
	Err       *errInfo
	dl        *Download
	cancel    context.CancelFunc
	fetchDone chan struct{} // 下载协程退出(半截文件已写完、句柄已关)时关闭
}

// waitFetchDone:暂停后马上点继续时,等上一个下载协程把半截文件写完、关掉,免得两个协程同时写一个文件。
func waitFetchDone(ch chan struct{}) {
	if ch == nil {
		return
	}
	select {
	case <-ch:
	case <-time.After(15 * time.Second):
	}
}

// checkModels:HEAD 一下每个已下载档位在 HF 上的同名文件(302 上带 X-Linked-Etag = sha256、X-Linked-Size),
// 再确保本地有指纹可比。只有网络整体不通时才返回错误;单个文件的 404 记在它自己身上。
func (u *updater) checkModels(ctx context.Context) error {
	base := u.models()
	var lastNetErr error
	okCount := 0
	for _, t := range u.cfg.Tiers {
		p := u.tierPath(t)
		st, err := os.Stat(p)
		if err != nil || st.IsDir() {
			continue
		}
		pctx, cancel := context.WithTimeout(ctx, 20*time.Second)
		d := &Download{URL: u.cfg.fileURL(base, t.File), Client: u.client, UserAgent: u.ua}
		meta, err := d.probe(pctx)
		cancel()
		rf := remoteFile{SHA256: meta.SHA256, Size: meta.Size, Endpoint: base, Checked: time.Now()}
		if rf.Size < 0 {
			rf.Size = 0
		}
		if err != nil {
			var pe *PermanentError
			if !errors.As(err, &pe) {
				lastNetErr = err
				continue // 网络问题:保留上次的结果
			}
			rf = remoteFile{Endpoint: base, Checked: time.Now(), Err: pe.Code}
		} else {
			okCount++
		}
		u.mu.Lock()
		u.st.Remote[t.File] = rf
		u.mu.Unlock()
		// 远端给了 sha、大小又和本地一样,才需要本地指纹(大小不同已经说明有新版)
		if rf.SHA256 != "" && (rf.Size <= 0 || rf.Size == st.Size()) {
			if _, err := u.localSHA(ctx, t.File, p); err != nil && ctx.Err() == nil {
				u.logf("算 %s 的指纹失败:%v", t.File, err)
			}
		}
	}
	if okCount == 0 && lastNetErr != nil {
		return lastNetErr
	}
	return nil
}

// localSHA 返回本地文件的 sha256:指纹表里有且大小/修改时间都对得上就直接用,否则算一遍(带进度)并记下。
// 0.3.x 下载的老文件没有指纹,第一次检查时会算一次,之后不再算。
func (u *updater) localSHA(ctx context.Context, file, path string) (string, error) {
	st, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	u.mu.Lock()
	fp, ok := u.fps[file]
	if ok && fp.Size == st.Size() && fp.ModNano == st.ModTime().UnixNano() {
		u.mu.Unlock()
		return fp.SHA256, nil
	}
	if u.hashing[file] != nil {
		u.mu.Unlock()
		return "", errors.New("已经在算了")
	}
	prog := &progress{Stage: "hash", Total: st.Size()}
	u.hashing[file] = prog
	u.mu.Unlock()
	defer func() {
		u.mu.Lock()
		delete(u.hashing, file)
		u.mu.Unlock()
	}()
	u.logf("给本地模型 %s 算指纹(只算这一次)", file)
	sum, err := hashFileProgress(ctx, path, func(done int64) {
		u.mu.Lock()
		prog.VerifyDone = done
		u.mu.Unlock()
	})
	if err != nil {
		return "", err
	}
	u.recordFP(file, path, sum)
	return sum, nil
}

func hashFileProgress(ctx context.Context, path string, onProgress func(int64)) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	buf := make([]byte, 4<<20)
	var done int64
	for {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		n, err := f.Read(buf)
		if n > 0 {
			h.Write(buf[:n])
			done += int64(n)
			if onProgress != nil {
				onProgress(done)
			}
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

// recordFP 记下一个本地文件的 sha256(下载校验通过、或算过一次之后)。
func (u *updater) recordFP(file, path, sum string) {
	if sum == "" {
		return
	}
	st, err := os.Stat(path)
	if err != nil {
		return
	}
	u.mu.Lock()
	u.fps[file] = fileFP{SHA256: sum, Size: st.Size(), ModNano: st.ModTime().UnixNano()}
	u.saveFPsLocked()
	u.mu.Unlock()
}

// restoreJobs:上次没下完的模型更新(<模型>.update.part)在状态里显示为已暂停,点继续接着下。
func (u *updater) restoreJobs() {
	u.restoreAppJob()
}

func (u *updater) startModelDownload(id string) error {
	t, ok := u.cfg.tier(id)
	if !ok {
		return &PermanentError{Code: "bad_tier", Msg: "没有这个档位"}
	}
	cur := u.tierPath(t)
	if !fileExists(cur) {
		return &PermanentError{Code: "not_downloaded", Msg: "这个档位还没下载过,不用更新"}
	}
	u.mu.Lock()
	if j := u.mjobs[id]; j != nil && (j.State == "downloading" || j.State == "waiting" || j.State == "installing") {
		u.mu.Unlock()
		return nil
	}
	for tid, j := range u.mjobs {
		if tid != id && (j.State == "downloading" || j.State == "waiting" || j.State == "installing") {
			u.mu.Unlock()
			return &PermanentError{Code: "busy", Msg: "另一个模型正在更新,等它完成再来"}
		}
	}
	rf, ok := u.st.Remote[t.File]
	var prev chan struct{}
	if j := u.mjobs[id]; j != nil {
		prev = j.fetchDone
	}
	u.mu.Unlock()
	if !ok || rf.Err != "" || (rf.SHA256 == "" && rf.Size <= 0) {
		return &PermanentError{Code: "need_check", Msg: "还没查到 HF 上的新版本,请先检查更新"}
	}
	waitFetchDone(prev)
	ctx, cancel := context.WithCancel(u.rootCtx)
	d := &Download{
		URL:        u.cfg.fileURL(u.models(), t.File),
		Dest:       cur + ".update",
		SHA256:     rf.SHA256,
		ExpectSize: rf.Size,
		UserAgent:  u.ua,
		Client:     u.client,
		Logf:       u.logf,
	}
	job := &modelJob{Tier: id, State: "downloading", dl: d, cancel: cancel, fetchDone: make(chan struct{})}
	u.mu.Lock()
	u.mjobs[id] = job
	u.mu.Unlock()
	u.logf("开始下载模型更新 %s ← %s", t.File, d.URL)
	go u.runModelJob(ctx, job, t)
	return nil
}

func (u *updater) runModelJob(ctx context.Context, job *modelJob, t Tier) {
	err := job.dl.Run(ctx)
	close(job.fetchDone)
	u.mu.Lock()
	if u.mjobs[t.ID] != job {
		u.mu.Unlock()
		return
	}
	switch {
	case errors.Is(err, context.Canceled):
		if job.State == "downloading" {
			job.State = "paused"
		}
		u.mu.Unlock()
		u.logf("模型更新已暂停(%s,已收到 %d 字节)", t.File, job.dl.Received.Load())
		return
	case err != nil:
		code := "network"
		var pe *PermanentError
		if errors.As(err, &pe) {
			code = pe.Code
		}
		job.State = "error"
		job.Err = &errInfo{Kind: "download", Code: code, Message: err.Error()}
		u.mu.Unlock()
		u.logf("模型更新下载失败:%v", err)
		return
	}
	job.State = "waiting"
	u.mu.Unlock()
	u.logf("模型更新已下好并校验通过:%s", job.dl.Dest)
	u.installModel(ctx, job, t, job.dl.Dest, job.dl.SHA)
}

// installModel:等手上的改写结束 → 替换文件(必要时重启引擎)→ 记下新指纹。
func (u *updater) installModel(ctx context.Context, job *modelJob, t Tier, newPath, sum string) {
	for u.eng.inflight() > 0 { // 正在改写:等它写完再动引擎
		if !sleepCtx(ctx, 500*time.Millisecond) {
			return
		}
	}
	u.mu.Lock()
	job.State = "installing"
	u.mu.Unlock()
	cur := u.tierPath(t)
	err := replaceModelFile(ctx, u.eng, t, cur, newPath, u.logf)
	u.mu.Lock()
	if err != nil {
		job.State = "error"
		job.Err = &errInfo{Kind: "install", Code: "install_failed", Message: err.Error()}
		u.mu.Unlock()
		u.logf("模型更新替换失败:%v", err)
		return
	}
	job.State = "done"
	u.mu.Unlock()
	if sum == "" {
		sum, _ = hashFileProgress(ctx, cur, nil)
	}
	u.recordFP(t.File, cur, sum)
	u.logf("模型 %s 已更新", t.File)
}

// replaceModelFile 用 newPath 换掉 cur,旧文件先改名成 .bak:
//   - 引擎没在用这个档位:直接换,删 .bak;
//   - 引擎在用:停引擎 → 换 → 起引擎 → 等就绪;起不来就停掉、换回旧文件、再起,返回错误。
//
// 任何一步改名失败都把文件恢复原样。
func replaceModelFile(ctx context.Context, eng engineCtl, t Tier, cur, newPath string, logf func(string, ...any)) error {
	bak := cur + ".bak"
	active := eng.usesTier(t.ID)
	if active {
		logf("引擎正在用 %s,先停下来再换文件", t.File)
		eng.stop()
	}
	restart := func() {
		if active {
			eng.start(t)
		}
	}
	_ = os.Remove(bak)
	if err := os.Rename(cur, bak); err != nil {
		restart()
		return fmt.Errorf("没法挪开旧模型文件:%w", err)
	}
	if err := os.Rename(newPath, cur); err != nil {
		if rerr := os.Rename(bak, cur); rerr != nil {
			logf("严重:恢复旧模型也失败了:%v", rerr)
		}
		restart()
		return fmt.Errorf("没法放入新模型文件:%w", err)
	}
	if !active {
		_ = os.Remove(bak)
		return nil
	}
	eng.start(t)
	if err := eng.waitSettled(ctx); err != nil {
		logf("新模型没能启动(%v),换回旧模型", err)
		eng.stop()
		bad := cur + ".rejected"
		_ = os.Remove(bad)
		if rerr := os.Rename(cur, bad); rerr != nil {
			logf("挪开新模型失败:%v", rerr)
		}
		if rerr := os.Rename(bak, cur); rerr != nil {
			logf("严重:换回旧模型失败:%v", rerr)
			return fmt.Errorf("新模型起不来,换回旧模型也失败了:%v", rerr)
		}
		_ = os.Remove(bad)
		eng.start(t)
		return errors.New("新模型没能启动,已换回原来的模型")
	}
	_ = os.Remove(bak)
	return nil
}

func (u *updater) pauseModel(id string) {
	u.mu.Lock()
	j := u.mjobs[id]
	if j != nil && j.State == "downloading" && j.cancel != nil {
		j.cancel()
		j.State = "paused"
	}
	u.mu.Unlock()
}

// cancelModel 放弃这次模型更新,删掉半截文件。
func (u *updater) cancelModel(id string) {
	t, ok := u.cfg.tier(id)
	if !ok {
		return
	}
	u.mu.Lock()
	j := u.mjobs[id]
	if j != nil && (j.State == "waiting" || j.State == "installing") {
		u.mu.Unlock()
		return // 已经在替换了,不能半路取消
	}
	if j != nil && j.cancel != nil {
		j.cancel()
	}
	delete(u.mjobs, id)
	u.mu.Unlock()
	p := u.tierPath(t) + ".update"
	if j != nil {
		waitFetchDone(j.fetchDone) // 让下载协程先放开文件
	}
	for _, f := range []string{p, p + ".part", p + ".part.json"} {
		_ = os.Remove(f)
	}
}

// ackModel 清掉一个已完成/出错的任务,让它回到普通的「已是最新/有新版本」显示。
func (u *updater) ackModel(id string) {
	u.mu.Lock()
	if j := u.mjobs[id]; j != nil && (j.State == "done" || j.State == "error") {
		delete(u.mjobs, id)
	}
	u.mu.Unlock()
}

// ───────────────────────── App 作为 engineCtl ─────────────────────────

type appEngine struct{ a *App }

func (e appEngine) usesTier(id string) bool {
	a := e.a
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.tier != id {
		return false
	}
	return a.phase == phaseStarting || a.phase == phaseReady ||
		(a.phase == phaseError && a.errInfo != nil && a.errInfo.Kind == "engine")
}

func (e appEngine) stop() { e.a.stopEngine() }

func (e appEngine) start(t Tier) {
	e.a.mu.Lock()
	e.a.restarts = 0
	e.a.mu.Unlock()
	e.a.startEngine(t)
}

func (e appEngine) waitSettled(ctx context.Context) error {
	for {
		e.a.mu.Lock()
		ph, ei := e.a.phase, e.a.errInfo
		e.a.mu.Unlock()
		switch ph {
		case phaseReady:
			return nil
		case phaseError:
			if ei != nil {
				return errors.New(ei.Message)
			}
			return errors.New("engine error")
		case phaseStarting:
		default: // 用户中途换了档位或退出:不算新模型的错
			return nil
		}
		if !sleepCtx(ctx, 300*time.Millisecond) {
			return ctx.Err()
		}
	}
}

func (e appEngine) inflight() int { return int(e.a.inflight.Load()) }

// 读一下 .update.part.json 里记的远端大小,给「已暂停」显示总量。
func partTotal(part string) int64 {
	b, err := os.ReadFile(part + ".json")
	if err != nil {
		return 0
	}
	var m remoteMeta
	if json.Unmarshal(b, &m) != nil {
		return 0
	}
	return m.Size
}
