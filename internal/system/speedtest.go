package system

// Native WAN speed test against speed.cloudflare.com (no external binary).
// Phases: latency (small GETs on one keep-alive connection), download and
// upload (parallel streams for a fixed duration, first second discarded as
// TCP warm-up). Only one test runs at a time.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptrace"
	"sort"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

const (
	speedHost     = "https://speed.cloudflare.com"
	speedStreams  = 4
	speedPhase    = 8 * time.Second
	speedWarmup   = 1 * time.Second
	speedSample   = 500 * time.Millisecond
	speedPings    = 12
	speedChunkDL  = 25_000_000
	speedChunkUL  = 1_000_000
	speedClientUA = "lab-router-speedtest/0.1"
)

type SpeedResult struct {
	Server        string    `json:"server"`
	Colo          string    `json:"colo,omitempty"`
	City          string    `json:"city,omitempty"`
	Country       string    `json:"country,omitempty"`
	ASN           string    `json:"asn,omitempty"`
	PublicIP      string    `json:"public_ip,omitempty"`
	LatencyMs     float64   `json:"latency_ms"`
	JitterMs      float64   `json:"jitter_ms"`
	LatencyLoss   int       `json:"latency_failed"`
	DownloadMbps  float64   `json:"download_mbps"`
	UploadMbps    float64   `json:"upload_mbps"`
	DownloadBytes int64     `json:"download_bytes"`
	UploadBytes   int64     `json:"upload_bytes"`
	DownSamples   []float64 `json:"download_samples"`
	UpSamples     []float64 `json:"upload_samples"`
	Streams       int       `json:"streams"`
	DurationS     float64   `json:"duration_s"`
	Time          time.Time `json:"time"`
}

var speedBusy sync.Mutex

var ErrSpeedBusy = errors.New("já existe um teste de velocidade em andamento")

func Speedtest(ctx context.Context) (*SpeedResult, error) {
	if !speedBusy.TryLock() {
		return nil, ErrSpeedBusy
	}
	defer speedBusy.Unlock()
	start := time.Now()
	tr := &http.Transport{Proxy: nil, MaxIdleConnsPerHost: speedStreams * 2, ForceAttemptHTTP2: false, TLSHandshakeTimeout: 8 * time.Second, ResponseHeaderTimeout: 8 * time.Second}
	defer tr.CloseIdleConnections()
	client := &http.Client{Transport: tr}
	res := &SpeedResult{Server: "speed.cloudflare.com", Streams: speedStreams, Time: start.UTC()}

	if e := speedLatency(ctx, client, res); e != nil {
		return nil, fmt.Errorf("servidor de teste inalcançável: %w", e)
	}
	var e error
	res.DownloadMbps, res.DownloadBytes, res.DownSamples, e = speedTransfer(ctx, client, false)
	if e != nil {
		return nil, fmt.Errorf("download falhou: %w", e)
	}
	res.UploadMbps, res.UploadBytes, res.UpSamples, e = speedTransfer(ctx, client, true)
	if e != nil {
		return nil, fmt.Errorf("upload falhou: %w", e)
	}
	res.DurationS = time.Since(start).Seconds()
	return res, nil
}

func speedRequest(ctx context.Context, method, url string, body io.Reader, size int64) (*http.Request, error) {
	r, e := http.NewRequestWithContext(ctx, method, url, body)
	if e != nil {
		return nil, e
	}
	r.Header.Set("User-Agent", speedClientUA)
	if body != nil {
		r.ContentLength = size
		r.Header.Set("Content-Type", "application/octet-stream")
	}
	return r, nil
}

// Latency = time from request written to first response byte, on a warm connection.
func speedLatency(ctx context.Context, c *http.Client, res *SpeedResult) error {
	var rtts []float64
	var lastErr error
	for i := 0; i <= speedPings; i++ {
		var wrote, first time.Time
		trace := &httptrace.ClientTrace{WroteRequest: func(httptrace.WroteRequestInfo) { wrote = time.Now() }, GotFirstResponseByte: func() { first = time.Now() }}
		pctx, cancel := context.WithTimeout(httptrace.WithClientTrace(ctx, trace), 4*time.Second)
		r, _ := speedRequest(pctx, "GET", speedHost+"/__down?bytes=0", nil, 0)
		resp, e := c.Do(r)
		if e != nil {
			cancel()
			lastErr = e
			if i > 0 {
				res.LatencyLoss++
			}
			continue
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		cancel()
		if i == 0 { // first request pays DNS/TCP/TLS; use it only for metadata
			h := resp.Header
			res.PublicIP, res.Colo, res.City, res.Country, res.ASN = h.Get("cf-meta-ip"), h.Get("cf-meta-colo"), h.Get("cf-meta-city"), h.Get("cf-meta-country"), h.Get("cf-meta-asn")
			continue
		}
		if !wrote.IsZero() && !first.IsZero() {
			rtts = append(rtts, float64(first.Sub(wrote).Microseconds())/1000)
		}
	}
	if len(rtts) == 0 {
		if lastErr == nil {
			lastErr = errors.New("sem medições")
		}
		return lastErr
	}
	for i := 1; i < len(rtts); i++ {
		d := rtts[i] - rtts[i-1]
		if d < 0 {
			d = -d
		}
		res.JitterMs += d
	}
	if len(rtts) > 1 {
		res.JitterMs /= float64(len(rtts) - 1)
	}
	sorted := append([]float64(nil), rtts...)
	sort.Float64s(sorted)
	res.LatencyMs = sorted[len(sorted)/2]
	return nil
}

// payloadReader streams a fixed-size body from a reusable buffer.
type payloadReader struct {
	left int64
	buf  []byte
}

func (r *payloadReader) Read(p []byte) (int, error) {
	if r.left <= 0 {
		return 0, io.EOF
	}
	n := len(p)
	if int64(n) > r.left {
		n = int(r.left)
	}
	n = copy(p[:n], r.buf)
	r.left -= int64(n)
	return n, nil
}

func speedTransfer(parent context.Context, c *http.Client, upload bool) (float64, int64, []float64, error) {
	ctx, cancel := context.WithTimeout(parent, speedPhase)
	defer cancel()
	// Upload bytes count only when the server confirms the whole body: the kernel
	// send buffer would otherwise absorb megabytes instantly and inflate the result.
	var total, lastDone atomic.Int64
	var errOnce sync.Once
	var firstErr error
	buf := make([]byte, 64<<10)
	for i := range buf {
		buf[i] = byte(i * 31)
	}
	var wg sync.WaitGroup
	for s := 0; s < speedStreams; s++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for ctx.Err() == nil {
				var r *http.Request
				if upload {
					r, _ = speedRequest(ctx, "POST", speedHost+"/__up", &payloadReader{left: speedChunkUL, buf: buf}, speedChunkUL)
				} else {
					r, _ = speedRequest(ctx, "GET", speedHost+"/__down?bytes="+strconv.Itoa(speedChunkDL), nil, 0)
				}
				resp, e := c.Do(r)
				if e != nil {
					if ctx.Err() == nil {
						errOnce.Do(func() { firstErr = e })
					}
					return
				}
				if upload {
					io.Copy(io.Discard, resp.Body)
					if resp.StatusCode == http.StatusOK {
						total.Add(speedChunkUL)
						lastDone.Store(time.Now().UnixNano())
					}
				} else {
					for {
						n, e := resp.Body.Read(buf)
						total.Add(int64(n))
						if e != nil {
							break
						}
					}
				}
				resp.Body.Close()
			}
		}()
	}
	start := time.Now()
	var samples []float64
	var warmBytes int64 = -1
	var warmAt time.Time
	prev := int64(0)
	interval := speedSample
	if upload {
		interval = time.Second // completions arrive in 1 MB steps; smooth the chart
	}
	tick := time.NewTicker(interval)
	for loop := true; loop; {
		select {
		case <-ctx.Done():
			loop = false
		case now := <-tick.C:
			cur := total.Load()
			samples = append(samples, float64(cur-prev)*8/interval.Seconds()/1e6)
			prev = cur
			if warmBytes < 0 && now.Sub(start) >= speedWarmup {
				warmBytes, warmAt = cur, now
			}
		}
	}
	tick.Stop()
	end := time.Now()
	wg.Wait()
	bytes := total.Load()
	if upload && lastDone.Load() > 0 {
		end = time.Unix(0, lastDone.Load())
	}
	if bytes == 0 {
		if firstErr != nil {
			return 0, 0, samples, firstErr
		}
		return 0, 0, samples, errors.New("nenhum dado transferido")
	}
	if warmBytes < 0 {
		warmBytes, warmAt = 0, start
	}
	window := end.Sub(warmAt).Seconds()
	if window <= 0 || bytes <= warmBytes {
		warmBytes, window = 0, end.Sub(start).Seconds()
	}
	mbps := float64(bytes-warmBytes) * 8 / window / 1e6
	return mbps, bytes, samples, nil
}
