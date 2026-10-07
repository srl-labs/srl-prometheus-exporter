package app

import (
	"context"
	"encoding/json"
	"fmt"

	log "github.com/sirupsen/logrus"
)

func (s *server) updateTelemetry(ctx context.Context, statePath string, jsData string) {
	log.Debugf("updating telemetry: %q: %s\n", statePath, jsData)
	if err := s.agent.UpdateState(statePath, jsData); err != nil {
		log.Errorf("could not update telemetry path=%s: err=%v", statePath, err)
	}
}

func (s *server) deleteTelemetry(ctx context.Context, statePath string) error {
	log.Debugf("deleting telemetry path %s", statePath)
	if err := s.agent.DeleteState(statePath); err != nil {
		log.Errorf("could not delete telemetry for path %s: %v", statePath, err)
		return err
	}
	return nil
}

func (s *server) updatePrometheusBaseTelemetry(ctx context.Context, cfg *baseConfig) {
	jsData, err := json.Marshal(cfg)
	if err != nil {
		log.Errorf("failed to marshal json data: %v", err)
		return
	}
	s.updateTelemetry(ctx, exporterStatePath, string(jsData))
}

// metrics
func (s *server) updateMetricTelemetry(ctx context.Context, name string, cfg *metricConfig) {
	jsData, err := json.Marshal(cfg)
	if err != nil {
		log.Errorf("failed to marshal json data: %v", err)
		return
	}
	s.updateTelemetry(ctx, fmt.Sprintf("%s[name=%s]", metricStatePath, name), string(jsData))
}

func (s *server) deleteMetricTelemetry(ctx context.Context, name string) {
	jsPath := fmt.Sprintf("%s[name=%s]", metricStatePath, name)
	log.Debugf("Deleting telemetry path %s", jsPath)
	s.deleteTelemetry(ctx, jsPath)
}

// custom metrics
func (s *server) updateCustomMetricTelemetry(ctx context.Context, name string, cfg *customMetricConfig) {
	jsData, err := json.Marshal(cfg)
	if err != nil {
		log.Errorf("failed to marshal json data: %v", err)
		return
	}
	s.updateTelemetry(ctx, fmt.Sprintf("%s[name=%s]", customMetricStatePath, name), string(jsData))
}

func (s *server) deleteCustomMetricTelemetry(ctx context.Context, name string) {
	jsPath := fmt.Sprintf("%s[name=%s]", customMetricStatePath, name)
	log.Debugf("Deleting telemetry path %s", jsPath)
	s.deleteTelemetry(ctx, jsPath)
}
