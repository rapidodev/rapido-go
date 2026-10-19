package tunneldoctor

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/legendary1205/rapido-go/internal/cache"
)

// KV is the small slice of Redis the job state needs.
type KV interface {
	Get(ctx context.Context, key string) (string, error)
	Set(ctx context.Context, key, value string, ttl time.Duration) error
	// SetNX stores the value only when the key does not exist and reports
	// whether it did.
	SetNX(ctx context.Context, key, value string, ttl time.Duration) (bool, error)
	Del(ctx context.Context, keys ...string) error
}

// RedisKV adapts the panel's cache client to KV.
type RedisKV struct{ C *cache.Client }

func (r RedisKV) Get(ctx context.Context, key string) (string, error) { return r.C.Get(ctx, key) }
func (r RedisKV) Set(ctx context.Context, key, value string, ttl time.Duration) error {
	return r.C.Set(ctx, key, value, ttl)
}
func (r RedisKV) SetNX(ctx context.Context, key, value string, ttl time.Duration) (bool, error) {
	return r.C.Raw().SetNX(ctx, key, value, ttl).Result()
}

func (r RedisKV) Del(ctx context.Context, keys ...string) error { return r.C.Del(ctx, keys...) }

// ErrNotFound is returned by Load when a tunnel has no diagnosis (or its
// stored state expired).
var ErrNotFound = errors.New("no diagnosis for this tunnel")

const (
	// JobTTL is how long a finished (or failed) diagnosis stays readable.
	JobTTL = time.Hour
	// lockTTL bounds how long a running diagnosis blocks a new one even if the
	// process running it died: longer than the slowest possible run.
	lockTTL = 4 * time.Minute
)

// Jobs keeps one diagnosis per tunnel in Redis, so any panel process can start
// one and any other can read it.
type Jobs struct {
	KV  KV
	Now func() time.Time
}

func (j Jobs) now() time.Time {
	if j.Now != nil {
		return j.Now().UTC()
	}
	return time.Now().UTC()
}

func jobKey(id int32) string  { return "tunneldoctor:job:" + strconv.Itoa(int(id)) }
func lockKey(id int32) string { return "tunneldoctor:lock:" + strconv.Itoa(int(id)) }

// Load returns the tunnel's latest diagnosis, or ErrNotFound.
func (j Jobs) Load(ctx context.Context, id int32) (*Job, error) {
	raw, err := j.KV.Get(ctx, jobKey(id))
	if errors.Is(err, redis.Nil) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	var job Job
	if err := json.Unmarshal([]byte(raw), &job); err != nil {
		return nil, err
	}
	// A "running" job whose lock is gone and that is older than the lock's
	// lifetime was lost with its process; report it as failed rather than
	// running forever.
	if job.Status == StatusRunning && j.now().Sub(job.StartedAt) > lockTTL+30*time.Second {
		job.Status = StatusFailed
		job.Error = "the diagnosis was interrupted (the panel restarted while it ran)"
		fin := j.now()
		job.FinishedAt = &fin
	}
	return &job, nil
}

func (j Jobs) save(ctx context.Context, id int32, job *Job) error {
	raw, err := json.Marshal(job)
	if err != nil {
		return err
	}
	return j.KV.Set(ctx, jobKey(id), string(raw), JobTTL)
}

// Start claims the tunnel for a new diagnosis. It returns started=false and the
// job already in progress when one is running.
func (j Jobs) Start(ctx context.Context, id int32) (started bool, current *Job, err error) {
	ok, err := j.KV.SetNX(ctx, lockKey(id), "1", lockTTL)
	if err != nil {
		return false, nil, err
	}
	if !ok {
		cur, _ := j.Load(ctx, id)
		return false, cur, nil
	}
	job := &Job{Status: StatusRunning, StartedAt: j.now()}
	if err := j.save(ctx, id, job); err != nil {
		return false, nil, err
	}
	return true, job, nil
}

// Finish records the outcome of a diagnosis started with Start.
func (j Jobs) Finish(ctx context.Context, id int32, startedAt time.Time, res *Result, runErr error) error {
	fin := j.now()
	job := &Job{StartedAt: startedAt, FinishedAt: &fin}
	if runErr != nil {
		job.Status, job.Error = StatusFailed, runErr.Error()
	} else {
		job.Status, job.Result = StatusDone, res
	}
	err := j.save(ctx, id, job)
	// Release the claim so the next diagnosis can start right away.
	if delErr := j.KV.Del(ctx, lockKey(id)); err == nil {
		err = delErr
	}
	return err
}
