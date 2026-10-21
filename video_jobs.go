package main

import (
	"crypto/rand"
	"encoding/hex"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Video job bookkeeping.
//
// CPA's /v1/videos answers with a job object that the client polls by id, and it
// pins the polling request to the credential that created the job. This upstream
// generates synchronously and has no job id of its own, so the plugin mints one
// and remembers the finished URL against it. A poll therefore returns the same
// completed job instead of creating a second clip — which is what would happen if
// the id were passed straight through to the generation path.

// videoJob is one finished clip.
type videoJob struct {
	URL       string
	Model     string
	CreatedAt time.Time
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

func (s *videoJobStore) put(id string, url string) {
	videoJobs.putWithModel(id, url, "")
}

func (s *videoJobStore) putWithModel(id, url, model string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.items[id] = videoJob{URL: url, Model: model, CreatedAt: time.Now()}
	s.gcLocked()
}

// get returns a job, reporting whether it is still known.
func (s *videoJobStore) get(id string) (videoJob, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	job, ok := s.items[strings.TrimSpace(id)]
	if !ok {
		return videoJob{}, false
	}
	if time.Since(job.CreatedAt) > s.maxLife {
		delete(s.items, id)
		return videoJob{}, false
	}
	return job, true
}

// gcLocked drops expired jobs, at most once a minute so a busy server does not
// pay for a full scan on every request.
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

// videoJobPayload renders a stored job in the shape CPA's video endpoint returns.
//
// The URL has to appear as video.url: CPA reads that path when a client asks for
// the file body, and treats a payload without it as a clip that is not ready. The
// top-level url and data[] entries are kept as well, so a client reading the job
// document directly does not have to know about the nested spelling.
func videoJobPayload(id string, job videoJob) map[string]any {
	return map[string]any{
		"id":         id,
		"object":     "video",
		"created_at": job.CreatedAt.Unix(),
		"status":     "completed",
		"model":      job.Model,
		"progress":   100,
		"url":        job.URL,
		"video_url":  job.URL,
		"video": map[string]any{
			"url": job.URL,
		},
		"data": []map[string]any{{
			"url":  job.URL,
			"type": "video",
		}},
	}
}

// fmtInt64 renders an integer for a JSON field without pulling strconv into the
// page-building code.
func fmtInt64(v int64) string {
	return strconv.FormatInt(v, 10)
}
