package app

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/golang/snappy"
	"github.com/openconfig/gnmi/proto/gnmi"
	"github.com/openconfig/gnmic/pkg/formatters"
	log "github.com/sirupsen/logrus"
	"github.com/vishvananda/netns"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/encoding/protowire"
)

const (
	defaultRemoteWriteInterval = 15 * time.Second
	defaultRemoteWriteTimeout  = 30 * time.Second
)

var errResubscribe = errors.New("remote write metrics changed")

type rwLabel struct {
	name  string
	value string
}

type rwPoint struct {
	labels []rwLabel
	value  float64
	ts     int64
	sentTS int64
}

// syncRemoteWrite starts or stops the SAMPLE subscription. Caller holds config.m.
func (s *server) syncRemoteWrite(ctx context.Context) {
	rw := s.config.baseConfig.RemoteWrite
	if rw == nil {
		rw = &remoteWrite{AdminState: adminDisable, OperState: operDown}
		s.config.baseConfig.RemoteWrite = rw
	}
	interval := rw.Interval.Value
	if interval == "" {
		interval = defaultRemoteWriteInterval.String()
	}
	on := s.config.baseConfig.AdminState == adminEnable &&
		rw.AdminState == adminEnable &&
		rw.URL.Value != ""
	if !on {
		s.cancelRemoteWriteLocked()
		rw.OperState = operDown
		return
	}
	profile := s.config.baseConfig.TLSProfile.Value
	if s.rwCancelFn != nil && s.rwURL == rw.URL.Value && s.rwInterval == interval && s.rwProfile == profile {
		return
	}
	s.cancelRemoteWriteLocked()
	s.rwURL = rw.URL.Value
	s.rwInterval = interval
	s.rwProfile = profile
	rw.OperState = operStarting
	rw.LastError.Value = ""
	go s.startRemoteWrite(ctx, s.rwGen)
}

func (s *server) cancelRemoteWriteLocked() {
	s.rwGen++
	s.rwURL = ""
	s.rwInterval = ""
	s.rwProfile = ""
	if s.rwCancelFn != nil {
		s.rwCancelFn()
		s.rwCancelFn = nil
	}
}

func (s *server) startRemoteWrite(ctx context.Context, gen uint64) {
	s.config.m.Lock()
	if gen != s.rwGen {
		s.config.m.Unlock()
		return
	}
	rw := s.config.baseConfig.RemoteWrite
	interval, err := time.ParseDuration(s.rwInterval)
	if err != nil || interval <= 0 {
		rw.OperState = operFailed
		rw.LastError.Value = fmt.Sprintf("invalid remote-write interval %q", s.rwInterval)
		cfg := s.config.baseConfig
		s.config.m.Unlock()
		s.updatePrometheusBaseTelemetry(ctx, cfg)
		return
	}
	timeout := defaultRemoteWriteTimeout
	if rw.Timeout.Value != "" {
		parsed, perr := time.ParseDuration(rw.Timeout.Value)
		if perr != nil || parsed <= 0 {
			rw.OperState = operFailed
			rw.LastError.Value = fmt.Sprintf("invalid remote-write timeout %q", rw.Timeout.Value)
			cfg := s.config.baseConfig
			s.config.m.Unlock()
			s.updatePrometheusBaseTelemetry(ctx, cfg)
			return
		}
		timeout = parsed
	}
	url, user, pass, profile := rw.URL.Value, rw.Username.Value, rw.Password.Value, s.rwProfile
	ctx, s.rwCancelFn = context.WithCancel(ctx)
	s.config.m.Unlock()

	log.Infof("remote write to %s every %s", url, interval)
	cache := make(map[string]*rwPoint)
	var cacheMu sync.Mutex
	for {
		if ctx.Err() != nil {
			return
		}
		err := s.sampleAndWrite(ctx, url, user, pass, profile, interval, timeout, cache, &cacheMu)
		if ctx.Err() != nil {
			return
		}
		if errors.Is(err, errResubscribe) {
			continue
		}
		msg := "remote write stopped"
		if err != nil {
			msg = err.Error()
			log.Errorf("remote write: %v", err)
		}
		s.setRemoteWriteState(operFailed, msg, false)
		select {
		case <-ctx.Done():
			return
		case <-time.After(retryInterval):
		}
	}
}

func (s *server) sampleAndWrite(ctx context.Context, url, user, pass, profile string, interval, timeout time.Duration, cache map[string]*rwPoint, cacheMu *sync.Mutex) error {
	metrics, sig := s.enabledMetricSnapshot()
	if len(metrics) == 0 {
		return fmt.Errorf("no metrics enabled")
	}
	nsName, ok := s.networkNamespace()
	if !ok {
		return fmt.Errorf("network instance %q not ready", s.config.baseConfig.NetworkInstance.Value)
	}
	ns, err := netns.GetFromName(nsName)
	if err != nil {
		return fmt.Errorf("network namespace %s: %w", nsName, err)
	}
	defer ns.Close()

	dialCtx, dialCancel := context.WithTimeout(ctx, retryInterval)
	conn, gnmiClient, err := s.createGNMIClient(dialCtx)
	dialCancel()
	if err != nil {
		return fmt.Errorf("gnmi dial: %w", err)
	}
	defer conn.Close()

	subCtx, subCancel := context.WithCancel(ctx)
	defer subCancel()
	if s.config.username != "" {
		subCtx = metadata.AppendToOutgoingContext(subCtx, "username", s.config.username)
	}
	if s.config.password != "" {
		subCtx = metadata.AppendToOutgoingContext(subCtx, "password", s.config.password)
	}

	errCh := make(chan error, len(metrics))
	for name, m := range metrics {
		name, m := name, m
		go func() {
			errCh <- s.recvSamples(subCtx, gnmiClient, name, m, interval, cache, cacheMu)
		}()
	}

	client, err := remoteWriteClient(ns, timeout, profile, strings.HasPrefix(url, "https://"))
	if err != nil {
		return err
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			_, now := s.enabledMetricSnapshot()
			if now != sig {
				return errResubscribe
			}
			if err := s.flushRemoteWrite(ctx, client, url, user, pass, cache, cacheMu); err != nil {
				s.setRemoteWriteState(operFailed, err.Error(), false)
				log.Errorf("remote write: %v", err)
				continue
			}
		case err := <-errCh:
			if err == nil || errors.Is(err, context.Canceled) {
				return nil
			}
			return err
		}
	}
}

func (s *server) recvSamples(ctx context.Context, client gnmi.GNMIClient, name string, m metric, interval time.Duration, cache map[string]*rwPoint, cacheMu *sync.Mutex) error {
	req, err := s.createSubscribeRequest(name, m, gnmi.SubscriptionList_STREAM, interval)
	if err != nil {
		return err
	}
	sub, err := client.Subscribe(ctx)
	if err != nil {
		return fmt.Errorf("subscribe %s: %w", name, err)
	}
	defer sub.CloseSend()
	if err := sub.Send(req); err != nil {
		return fmt.Errorf("subscribe %s: %w", name, err)
	}
	for {
		resp, err := sub.Recv()
		if err != nil {
			return fmt.Errorf("subscribe %s: %w", name, err)
		}
		events, err := formatters.ResponseToEventMsgs("", resp, nil)
		if err != nil {
			return fmt.Errorf("subscribe %s: %w", name, err)
		}
		for _, ev := range events {
			s.storeSample(cache, cacheMu, name, ev)
		}
	}
}

func (s *server) storeSample(cache map[string]*rwPoint, cacheMu *sync.Mutex, metricName string, ev *formatters.EventMsg) {
	// Stamp samples when they arrive. gNMI's timestamp is the last change,
	// which is older than a sample Prometheus already stored and is rejected
	// as out of order.
	ts := time.Now().UnixMilli()
	labels, values := s.getLabels(ev)
	for vname, raw := range ev.Values {
		value, err := getFloat(raw)
		if err != nil {
			continue
		}
		pointLabels := make([]rwLabel, 0, len(labels)+1)
		pointLabels = append(pointLabels, rwLabel{name: "__name__", value: s.metricName(metricName, vname)})
		for i := range labels {
			pointLabels = append(pointLabels, rwLabel{name: labels[i], value: values[i]})
		}
		sort.Slice(pointLabels, func(i, j int) bool { return pointLabels[i].name < pointLabels[j].name })
		key := seriesKey(pointLabels)
		cacheMu.Lock()
		prev := cache[key]
		if prev == nil || ts >= prev.ts {
			sent := int64(0)
			if prev != nil {
				sent = prev.sentTS
			}
			cache[key] = &rwPoint{labels: pointLabels, value: value, ts: ts, sentTS: sent}
		}
		cacheMu.Unlock()
	}
}

func seriesKey(labels []rwLabel) string {
	var b strings.Builder
	for _, l := range labels {
		b.WriteString(l.name)
		b.WriteByte('=')
		b.WriteString(l.value)
		b.WriteByte('\xff')
	}
	return b.String()
}

func (s *server) flushRemoteWrite(ctx context.Context, client *http.Client, url, user, pass string, cache map[string]*rwPoint, cacheMu *sync.Mutex) error {
	cacheMu.Lock()
	pending := make([]*rwPoint, 0, len(cache))
	for _, p := range cache {
		if p.ts > p.sentTS {
			pending = append(pending, p)
		}
	}
	cacheMu.Unlock()
	if len(pending) == 0 {
		return nil
	}
	body := snappy.Encode(nil, marshalWriteRequest(pending))
	reqCtx, cancel := context.WithTimeout(ctx, client.Timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-protobuf")
	req.Header.Set("Content-Encoding", "snappy")
	req.Header.Set("X-Prometheus-Remote-Write-Version", "0.1.0")
	if user != "" {
		req.SetBasicAuth(user, pass)
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("remote write %s: %s", resp.Status, strings.TrimSpace(string(msg)))
	}
	cacheMu.Lock()
	for _, p := range pending {
		if p.sentTS < p.ts {
			p.sentTS = p.ts
		}
	}
	cacheMu.Unlock()
	s.setRemoteWriteState(operUp, "", true)
	log.Infof("remote write: sent %d series", len(pending))
	return nil
}

func marshalWriteRequest(points []*rwPoint) []byte {
	var buf []byte
	for _, p := range points {
		series := marshalTimeSeries(p)
		buf = protowire.AppendTag(buf, 1, protowire.BytesType)
		buf = protowire.AppendBytes(buf, series)
	}
	return buf
}

func marshalTimeSeries(p *rwPoint) []byte {
	var buf []byte
	for _, l := range p.labels {
		lab := protowire.AppendTag(nil, 1, protowire.BytesType)
		lab = protowire.AppendString(lab, l.name)
		lab = protowire.AppendTag(lab, 2, protowire.BytesType)
		lab = protowire.AppendString(lab, l.value)
		buf = protowire.AppendTag(buf, 1, protowire.BytesType)
		buf = protowire.AppendBytes(buf, lab)
	}
	sample := protowire.AppendTag(nil, 1, protowire.Fixed64Type)
	sample = protowire.AppendFixed64(sample, math.Float64bits(p.value))
	sample = protowire.AppendTag(sample, 2, protowire.VarintType)
	sample = protowire.AppendVarint(sample, uint64(p.ts))
	buf = protowire.AppendTag(buf, 2, protowire.BytesType)
	buf = protowire.AppendBytes(buf, sample)
	return buf
}

func remoteWriteClient(ns netns.NsHandle, timeout time.Duration, profile string, https bool) (*http.Client, error) {
	var tlsCfg *tls.Config
	if https && profile != "" {
		cfg, err := loadTLSConfig(profile)
		if err != nil {
			return nil, err
		}
		tlsCfg = cfg
	}
	transport := &http.Transport{
		Proxy:           nil,
		TLSClientConfig: tlsCfg,
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			runtime.LockOSThread()
			defer runtime.UnlockOSThread()
			orig, err := netns.Get()
			if err != nil {
				return nil, err
			}
			defer orig.Close()
			if err := netns.Set(ns); err != nil {
				return nil, err
			}
			defer netns.Set(orig)
			return (&net.Dialer{Timeout: timeout}).DialContext(ctx, network, address)
		},
	}
	return &http.Client{Timeout: timeout, Transport: transport}, nil
}

func (s *server) enabledMetricSnapshot() (map[string]metric, string) {
	s.config.m.Lock()
	defer s.config.m.Unlock()
	out := make(map[string]metric)
	parts := make([]string, 0)
	add := func(name string, m metric) {
		out[name] = m
		paths := make([]string, len(m.Paths))
		for i, p := range m.Paths {
			paths[i] = p.Value
		}
		sort.Strings(paths)
		parts = append(parts, name+"="+strings.Join(paths, ","))
	}
	for name, m := range s.config.metrics {
		if m.Metric.State == adminEnable {
			add(name, m.Metric)
		}
	}
	for name, m := range s.config.customMetric {
		if m.Metric.State == adminEnable {
			add(name, m.Metric)
		}
	}
	sort.Strings(parts)
	return out, strings.Join(parts, ";")
}

func (s *server) setRemoteWriteState(oper, lastErr string, inc bool) {
	s.config.m.Lock()
	rw := s.config.baseConfig.RemoteWrite
	if rw == nil {
		s.config.m.Unlock()
		return
	}
	rw.OperState = oper
	rw.LastError.Value = lastErr
	if inc {
		rw.WritesCount.Value++
	}
	cfg := s.config.baseConfig
	s.config.m.Unlock()
	s.updatePrometheusBaseTelemetry(context.Background(), cfg)
}
