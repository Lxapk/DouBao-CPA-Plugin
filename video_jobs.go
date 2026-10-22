package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"strconv"
	"strings"
	"sync"
	"time"
)

// 视频任务的异步执行。
//
// 上游同步生成一段视频要 5–10 分钟，而客户端的 HTTP 超时通常比这短得多，
// 于是等结果的那次请求会被客户端自己掐断 —— 生成其实成功了，结果却没人收。
//
// CPA 的 /v1/videos 本来就是"提交任务 + 轮询"的形态，所以这里改成真异步：
// 提交时立即返回 queued，后台 goroutine 去跑，客户端按 id 轮询。
// 轮询期间任务还在跑就返回 in_progress，跑完返回 completed 与地址。
//
// 与之前"同步跑完再报完成"相比，区别只在于等待发生在轮询请求之间，而不是
// 压在客户端那一个 HTTP 请求上。

// videoJobStatus 是任务状态。
type videoJobStatus string

const (
	jobQueued     videoJobStatus = "queued"
	jobInProgress videoJobStatus = "in_progress"
	jobCompleted  videoJobStatus = "completed"
	jobFailed     videoJobStatus = "failed"
)

// videoJob is one clip the plugin is producing.
type videoJob struct {
	ID        string
	Status    videoJobStatus
	URL       string
	Model     string
	Seconds   int
	Progress  int
	Error     string
	CreatedAt time.Time
	Finished  time.Time

	// 凭据是提交时的那一份：轮询请求未必带着同一个账号，生成又必须用原账号
	// 去查会话，所以这里留一份。
	creds *credentials
	// prompt 与 mode 是后台执行的输入。
	prompt string
	mode   generationMode
	// conversationID 是补读结果用的：流断开后要靠它回查会话。
	conversationID string
}

type videoJobStore struct {
	mu      sync.Mutex
	items   map[string]videoJob
	lastGC  time.Time
	maxLife time.Duration
}

var videoJobs = &videoJobStore{
	items:   map[string]videoJob{},
	maxLife: 6 * time.Hour,
}

func newVideoJobID() string {
	var b [12]byte
	if _, errRead := rand.Read(b[:]); errRead != nil {
		return "video-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	}
	return "video-" + hex.EncodeToString(b[:])
}

// submit 登记一个任务并在后台开始执行。
func (s *videoJobStore) submit(job videoJob) videoJob {
	s.mu.Lock()
	job.Status = jobQueued
	job.CreatedAt = time.Now()
	job.Progress = 0
	s.items[job.ID] = job
	s.gcLocked()
	s.mu.Unlock()

	go runVideoJob(job.ID)
	return job
}

// update 改写任务的若干字段。
func (s *videoJobStore) update(id string, fn func(*videoJob)) videoJob {
	s.mu.Lock()
	defer s.mu.Unlock()
	job, ok := s.items[id]
	if !ok {
		return videoJob{}
	}
	fn(&job)
	s.items[id] = job
	return job
}

// get 返回任务，报告是否存在。
//
// 过期的任务被当作不存在：结果只在内存里，重启或超期后重生成才是唯一出路，
// 返回一个永远停在 in_progress 的壳只会让客户端无限轮询。
func (s *videoJobStore) get(id string) (videoJob, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	job, ok := s.items[strings.TrimSpace(id)]
	if !ok {
		return videoJob{}, false
	}
	if time.Since(job.CreatedAt) > s.maxLife {
		delete(s.items, strings.TrimSpace(id))
		return videoJob{}, false
	}
	return job, true
}

// gcLocked 清理过期任务，最多每分钟一次。
func (s *videoJobStore) gcLocked() {
	if time.Since(s.lastGC) < time.Minute {
		return
	}
	s.lastGC = time.Now()
	for id, job := range s.items {
		if time.Since(job.CreatedAt) > s.maxLife {
			delete(s.items, id)
		}
	}
}

// runVideoJob 是任务的执行体。
func runVideoJob(id string) {
	job, ok := videoJobs.get(id)
	if !ok {
		return
	}
	videoJobs.update(id, func(j *videoJob) { j.Status = jobInProgress })

	res, errRun := generateVideo(job.creds, job.prompt, job.mode, job.conversationID)

	videoJobs.update(id, func(j *videoJob) {
		j.Finished = time.Now()
		if errRun != nil {
			j.Status = jobFailed
			j.Error = errRun.Error()
			return
		}
		if res.URL == "" {
			j.Status = jobFailed
			j.Error = "上游没有返回视频：生成已结束但会话里没有可用的片段地址。" +
				"这类情况通常是额度不足或生成被上游拦截。"
			return
		}
		j.Status = jobCompleted
		j.URL = res.URL
		j.Model = res.Model
		j.Seconds = res.Seconds
		j.Progress = 100
	})
}

// videoResult 是后台执行的结果。
type videoResult struct {
	URL     string
	Model   string
	Seconds int
}

// generateVideo 跑一次真实生成，并在流断开后回查会话拿结果。
//
// budget 是「等结果」的总预算，从发起请求算起；豆包生成视频通常要 5–10 分钟，
// 期间 SSE 连接会先断掉，所以流结束不代表失败。
func generateVideo(creds *credentials, prompt string, mode generationMode, knownConvID string) (videoResult, error) {
	settings := state.settings.get()
	budget := settings.MediaPollSeconds
	if budget <= 0 {
		budget = 900
	}
	deadline := time.Now().Add(time.Duration(budget) * time.Second)
	ctx, cancel := context.WithTimeout(context.Background(), time.Until(deadline))
	defer cancel()

	conversationID := strings.TrimSpace(knownConvID)
	if conversationID == "" {
		if id, errConv := newClient(creds).latestConversationID(ctx); errConv == nil && id != "" {
			conversationID = id
		}
	}

	result, errRun := runCompletionWithImages(ctx, creds, prompt, mode, conversationID, conversationID == "", nil, nil)
	// 流被上游断掉是常态：这里接着回查会话，而不是把它当失败返回。
	if errRun != nil && !isStreamClosed(errRun) {
		return videoResult{}, errRun
	}

	out := videoResult{Model: mode.ModelKey}
	if result != nil {
		for _, asset := range result.assets {
			if asset.URL == "" {
				continue
			}
			if asset.isVideo() {
				out.URL = asset.URL
				out.Seconds = asset.Duration
			} else if out.URL == "" {
				out.URL = asset.URL
			}
		}
	}
	if out.URL != "" {
		return out, nil
	}

	// 流里没有片段：说明连接在生成完成前就断了，回查会话等结果。
	if conversationID == "" {
		return out, nil
	}
	asset, ok := waitForVideoAsset(ctx, creds, conversationID, time.Until(deadline))
	if !ok {
		return out, nil
	}
	out.URL = asset.URL
	out.Seconds = asset.Duration
	if asset.Model != "" {
		out.Model = asset.Model
	}
	return out, nil
}

// isStreamClosed 判断错误是不是「连接被上游关掉」。
//
// 视频生成时这是正常现象：上游先推一段文字，然后主动结束 SSE，成品稍后才在
// 会话里出现。把它当失败会让每一次成功的生成都报错。
func isStreamClosed(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	for _, needle := range []string{
		"unexpected eof", "eof", "connection reset", "stream error",
		"context canceled", "context deadline exceeded", "broken pipe",
	} {
		if strings.Contains(msg, needle) {
			return true
		}
	}
	return false
}

// videoJobPayload 把任务渲染成 CPA 视频端点的返回结构。
//
// 未完成的任务也返回 200 与真实状态，而不是报错：客户端应当继续轮询，
// 而不是把"还在生成"当成失败。
func videoJobPayload(job videoJob, seconds int) map[string]any {
	out := map[string]any{
		"id":         job.ID,
		"object":     "video",
		"created_at": job.CreatedAt.Unix(),
		"status":     string(job.Status),
		"model":      job.Model,
		"progress":   job.Progress,
	}
	if seconds > 0 {
		out["seconds"] = fmtInt64(int64(seconds))
	}
	switch job.Status {
	case jobCompleted:
		out["completed_at"] = job.Finished.Unix()
		out["url"] = job.URL
		out["video_url"] = job.URL
		out["video"] = map[string]any{"url": job.URL}
		out["data"] = []map[string]any{{"url": job.URL, "type": "video"}}
	case jobFailed:
		out["error"] = map[string]any{"message": job.Error, "code": "generation_failed"}
	}
	return out
}

// fmtInt64 把整数渲染成 JSON 字段值。
func fmtInt64(v int64) string {
	return strconv.FormatInt(v, 10)
}
