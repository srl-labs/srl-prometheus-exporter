package app

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/nokia/srlinux-ndk-go/ndk"
	log "github.com/sirupsen/logrus"
	"github.com/srl-labs/bond"
)

const (
	operUp       = "OPER_STATE_up"
	operDown     = "OPER_STATE_down"
	operStarting = "OPER_STATE_starting"
	operFailed   = "OPER_STATE_failed"

	adminEnable  = "ADMIN_STATE_enable"
	adminDisable = "ADMIN_STATE_disable"

	commitEndPath = ".commit.end"

	// Config notifications from bond use YANG names with hyphens.
	exporterPath     = "/system/prometheus-exporter"
	metricPath       = "/system/prometheus-exporter/metric"
	customMetricPath = "/system/prometheus-exporter/custom-metric"

	// State paths keep NDK underscores. bond converts '/' and list keys,
	// and leaves '_' in place, which is the js_path form SR Linux expects.
	exporterStatePath     = "/system/prometheus_exporter"
	metricStatePath       = "/system/prometheus_exporter/metric"
	customMetricStatePath = "/system/prometheus_exporter/custom_metric"

	opCreate = "SDK_MGR_OPERATION_CREATE"
	opUpdate = "SDK_MGR_OPERATION_UPDATE"
	opDelete = "SDK_MGR_OPERATION_DELETE"
)

type stringValue struct {
	Value string `json:"value,omitempty"`
}

type uint64Value struct {
	Value uint64 `json:"value,omitempty"`
}

type boolValue struct {
	Value bool `json:"value,omitempty"`
}

type config struct {
	agentName string

	baseConfig *baseConfig

	m            *sync.Mutex
	trx          []*bond.ConfigNotification
	nwInst       map[string]*ndk.NetworkInstanceData
	metrics      map[string]*metricConfig
	customMetric map[string]*customMetricConfig

	// from file
	username string
	password string
	// cliDebug stays on when the process was started with -d.
	cliDebug bool
	debug    bool
}

type FileConfig struct {
	Metrics  map[string][]string `yaml:"metrics,omitempty"`
	Username string              `yaml:"username,omitempty"`
	Password string              `yaml:"password,omitempty"`
}

func NewConfig(fc *FileConfig, agentName string, debug bool) *config {
	if len(fc.Metrics) > 0 {
		knownMetrics = fc.Metrics
	}

	kmetrics := make(map[string]*metricConfig)
	for n := range knownMetrics {
		kmetrics[n] = &metricConfig{}
		kmetrics[n].Metric.State = adminDisable
	}
	bcfg := &baseConfig{
		AdminState: adminDisable,
		OperState:  operDown,
	}

	return &config{
		agentName:    agentName,
		baseConfig:   bcfg,
		m:            new(sync.Mutex),
		nwInst:       make(map[string]*ndk.NetworkInstanceData),
		metrics:      kmetrics,
		customMetric: make(map[string]*customMetricConfig),
		username:     fc.Username,
		password:     fc.Password,
		cliDebug:     debug,
		debug:        debug,
	}
}

type baseConfig struct {
	AdminState      string        `json:"admin_state,omitempty"`
	Debug           string        `json:"debug,omitempty"`
	OperState       string        `json:"oper_state,omitempty"`
	NetworkInstance stringValue   `json:"network_instance,omitempty"`
	TLSProfile      stringValue   `json:"tls_profile,omitempty"`
	GRPCServer      stringValue   `json:"grpc_server,omitempty"`
	Scrape          *scrapeConfig `json:"scrape,omitempty"`
	RemoteWrite     *remoteWrite  `json:"remote_write,omitempty"`
	Registration    *registration `json:"registration,omitempty"`
}

// scrapeOn reports whether the HTTP listener should run.
// The YANG default is enable, including when the container is omitted.
func scrapeOn(sc *scrapeConfig) bool {
	return sc == nil || sc.AdminState == "" || sc.AdminState == adminEnable
}

type scrapeConfig struct {
	AdminState   string      `json:"admin_state,omitempty"`
	Address      stringValue `json:"address,omitempty"`
	Port         stringValue `json:"port,omitempty"`
	HttpPath     stringValue `json:"http_path,omitempty"`
	OperState    string      `json:"oper_state,omitempty"`
	ScrapesCount uint64Value `json:"scrapes_count,omitempty"`
}

type metricConfig struct {
	Metric metric `json:"metric,omitempty"`
}

type customMetricConfig struct {
	Metric metric `json:"custom_metric,omitempty"`
}

type metric struct {
	State    string        `json:"admin_state,omitempty"`
	HelpText stringValue   `json:"help_text,omitempty"`
	Paths    []stringValue `json:"paths,omitempty"`
}

type remoteWrite struct {
	URL         stringValue `json:"url,omitempty"`
	Interval    stringValue `json:"interval,omitempty"`
	Timeout     stringValue `json:"timeout,omitempty"`
	Username    stringValue `json:"username,omitempty"`
	Password    stringValue `json:"password,omitempty"`
	AdminState  string      `json:"admin_state,omitempty"`
	OperState   string      `json:"oper_state,omitempty"`
	WritesCount uint64Value `json:"writes_count,omitempty"`
	LastError   stringValue `json:"last_error,omitempty"`
}

type registration struct {
	Address    stringValue   `json:"address,omitempty"`
	Username   stringValue   `json:"username,omitempty"`
	Password   stringValue   `json:"password,omitempty"`
	Token      stringValue   `json:"token,omitempty"`
	TTL        stringValue   `json:"ttl,omitempty"`
	HTTPCheck  boolValue     `json:"http-check,omitempty"`
	Tags       []stringValue `json:"tags,omitempty"`
	AdminState string        `json:"admin_state,omitempty"`
	OperState  string        `json:"oper_state,omitempty"`
}

func (s *server) ConfigHandler(ctx context.Context) {
	// Network-instance notifications need a second NDK stream. The server
	// rejects that create, so the namespace is resolved as srbase-<name>.
	for {
		select {
		case nwInst, ok := <-s.agent.Notifications.NwInst:
			if !ok {
				return
			}
			if s.config.debug {
				log.Debugf("NwInst notification: %+v", nwInst)
			}
			s.handleNwInstCfg(ctx, nwInst)
		case cfg, ok := <-s.agent.Notifications.Config:
			if !ok {
				return
			}
			if s.config.debug {
				log.Debugf("Config notification: %+v", cfg)
			}
			s.handleConfigEvent(ctx, cfg)
		case <-ctx.Done():
			return
		}
	}
}

func (s *server) handleConfigEvent(ctx context.Context, cfg *bond.ConfigNotification) {
	s.config.m.Lock()
	defer s.config.m.Unlock()

	log.Debugf("handling cfg: %+v", cfg)
	log.Debugf("PATH: %s\n", cfg.PathWithoutKeys)
	log.Debugf("KEYS: %v\n", cfg.Keys)
	log.Debugf("JSON:\n%s\n", cfg.Json)

	// collect non commit.end config notifications
	if cfg.Path != commitEndPath {
		s.config.trx = append(s.config.trx, cfg)
		return
	}
	// when the path is ".commit.end", handle the stored config notifications
	for _, txCfg := range s.config.trx {
		switch txCfg.PathWithoutKeys {
		case exporterPath:
			switch txCfg.Op {
			case opCreate:
				s.handleCfgPrometheusCreate(ctx, txCfg)
			case opUpdate:
				s.handleCfgPrometheusChange(ctx, txCfg)
			case opDelete:
				log.Errorf("received delete Operation for path %q, this is unexpected...", exporterPath)
			}
		case metricPath:
			if len(txCfg.Keys) == 0 {
				log.Errorf("%q no keys in cfg notification: %+v", metricPath, txCfg)
				continue
			}
			switch txCfg.Op {
			case opCreate:
				s.handleCfgMetricCreate(ctx, txCfg)
			case opUpdate:
				s.handleCfgMetricChange(ctx, txCfg)
			case opDelete:
				s.handleCfgMetricDelete(ctx, txCfg)
			}
		case customMetricPath:
			if len(txCfg.Keys) == 0 {
				log.Errorf("%q no keys in cfg notification: %+v", customMetricPath, txCfg)
				continue
			}
			switch txCfg.Op {
			case opUpdate:
				s.handleCfgCustomMetricCreateChange(ctx, txCfg)
			case opCreate:
				s.handleCfgCustomMetricCreateChange(ctx, txCfg)
			case opDelete:
				s.handleCfgCustomMetricDelete(ctx, txCfg)
			}
		default:
			log.Errorf("unexpected config path %q", txCfg.PathWithoutKeys)
		}
	}
	s.config.trx = make([]*bond.ConfigNotification, 0)
}

func (s *server) handleCfgPrometheusCreate(ctx context.Context, cfg *bond.ConfigNotification) {
	newCfg := &baseConfig{
		Scrape: &scrapeConfig{
			AdminState: adminEnable,
			OperState:  operDown,
		},
		RemoteWrite: &remoteWrite{
			AdminState: adminDisable,
			OperState:  operDown,
		},
		Registration: &registration{
			AdminState: adminDisable,
			OperState:  operDown,
		},
	}
	err := json.Unmarshal([]byte(cfg.Json), newCfg)
	if err != nil {
		log.Errorf("failed to marshal config data from path %q: %v", cfg.PathWithoutKeys, err)
		return
	}
	if s.config.debug {
		b, err := json.MarshalIndent(newCfg, "", "  ")
		if err != nil {
			log.Errorf("failed to Marshal baseconfig: %v", err)
			return
		}
		log.Debugf("read baseconfig data: %s", string(b))
	}

	// set default oper state
	newCfg.OperState = operDown
	// store initial config
	s.config.baseConfig = newCfg
	s.applyDebug(newCfg.Debug)

	s.syncScrape(ctx)
	s.syncRemoteWrite(ctx)
	// update internal telemetry status
	s.updatePrometheusBaseTelemetry(ctx, newCfg)
}

func (s *server) handleCfgPrometheusChange(ctx context.Context, cfg *bond.ConfigNotification) {
	newCfg := &baseConfig{
		Scrape:       new(scrapeConfig),
		RemoteWrite:  new(remoteWrite),
		Registration: new(registration),
	}
	err := json.Unmarshal([]byte(cfg.Json), newCfg)
	if err != nil {
		log.Errorf("failed to marshal config data from path %q: %v", cfg.PathWithoutKeys, err)
		return
	}
	if s.config.debug {
		b, err := json.MarshalIndent(newCfg, "", "  ")
		if err != nil {
			log.Errorf("failed to Marshal baseconfig: %v", err)
			return
		}
		log.Debugf("read baseconfig data: %s", string(b))
	}

	s.applyDebug(newCfg.Debug)

	prevOper := s.config.baseConfig.OperState
	prevRegOper := operDown
	if s.config.baseConfig.Registration != nil {
		prevRegOper = s.config.baseConfig.Registration.OperState
	}
	if newCfg.Registration == nil {
		newCfg.Registration = &registration{}
	}
	prevRW := remoteWrite{AdminState: adminDisable, OperState: operDown}
	if s.config.baseConfig.RemoteWrite != nil {
		prevRW = *s.config.baseConfig.RemoteWrite
	}
	if newCfg.RemoteWrite == nil {
		newCfg.RemoteWrite = &remoteWrite{}
	}
	prevScrape := scrapeConfig{AdminState: adminEnable, OperState: operDown}
	if s.config.baseConfig.Scrape != nil {
		prevScrape = *s.config.baseConfig.Scrape
	}
	if newCfg.Scrape == nil {
		newCfg.Scrape = &scrapeConfig{AdminState: adminEnable}
	}
	newCfg.OperState = prevOper
	newCfg.Registration.OperState = prevRegOper
	newCfg.RemoteWrite.OperState = prevRW.OperState
	newCfg.RemoteWrite.WritesCount = prevRW.WritesCount
	newCfg.RemoteWrite.LastError = prevRW.LastError
	newCfg.Scrape.OperState = prevScrape.OperState
	newCfg.Scrape.ScrapesCount = prevScrape.ScrapesCount
	s.config.baseConfig = newCfg

	if newCfg.AdminState == adminDisable && prevOper != operDown {
		s.shutdown(ctx, time.Second/2)
		return
	}
	s.syncRemoteWrite(ctx)
	s.syncScrape(ctx)

	// HTTP server already running, check if registration has to be started or stopped
	if s.srv != nil && newCfg.AdminState == adminEnable && scrapeOn(newCfg.Scrape) {
		log.Debug("server is up, checking if registration needs to be started...")
		// server is already up, check if registration needs to be started
		if newCfg.Registration.AdminState == adminEnable && prevRegOper == operDown {
			go s.registerService(ctx)
		} else if newCfg.Registration.AdminState == adminDisable && prevOper != operUp {
			if s.regCancelFn != nil {
				s.regCancelFn()
			}
		}
	}

	s.updatePrometheusBaseTelemetry(ctx, s.config.baseConfig)
}

// syncScrape starts or stops the HTTP listener. Caller holds config.m.
func (s *server) syncScrape(ctx context.Context) {
	b := s.config.baseConfig
	if b.Scrape == nil {
		b.Scrape = &scrapeConfig{AdminState: adminEnable, OperState: operDown}
	}
	profile := b.TLSProfile.Value
	if b.AdminState != adminEnable || !scrapeOn(b.Scrape) {
		s.stopListenerLocked()
		b.Scrape.OperState = operDown
		if b.AdminState == adminEnable {
			b.OperState = operUp
		}
		return
	}
	if s.srv != nil && b.Scrape.OperState == operUp && s.scrapeProfile == profile {
		return
	}
	s.stopListenerLocked()
	s.scrapeProfile = profile
	b.OperState = operStarting
	b.Scrape.OperState = operStarting
	go s.start(ctx)
}

func (s *server) handleCfgMetricCreate(ctx context.Context, cfg *bond.ConfigNotification) {
	key := cfg.Keys[0]
	newMetricConfig := new(metricConfig)
	err := json.Unmarshal([]byte(cfg.Json), newMetricConfig)
	if err != nil {
		log.Errorf("failed to marshal config data from path %s: %v", cfg.PathWithoutKeys, err)
		return
	}
	log.Debugf("read metric config data: %+v", newMetricConfig)

	if _, ok := s.config.metrics[key]; !ok {
		s.config.metrics[key] = new(metricConfig)
	}
	log.Debugf("looking for known metrics with key : %s", key)
	if knownMetricPaths, ok := knownMetrics[key]; ok {
		log.Debugf("found known metric paths: %+v", knownMetricPaths)
		newMetricConfig.Metric.Paths = make([]stringValue, len(knownMetricPaths))
		for i, p := range knownMetricPaths {
			newMetricConfig.Metric.Paths[i].Value = p
		}
	}
	// store new config
	s.config.metrics[key] = newMetricConfig
	// update metric telemetry
	s.updateMetricTelemetry(ctx, key, newMetricConfig)
}

func (s *server) handleCfgMetricChange(ctx context.Context, cfg *bond.ConfigNotification) {
	key := cfg.Keys[0]
	newMetricConfig := new(metricConfig)
	err := json.Unmarshal([]byte(cfg.Json), newMetricConfig)
	if err != nil {
		log.Errorf("failed to marshal config data from path %s: %v", cfg.PathWithoutKeys, err)
		return
	}

	// store new config
	s.config.metrics[key].Metric.State = newMetricConfig.Metric.State
	// update metric telemetry
	s.updateMetricTelemetry(ctx, key, newMetricConfig)
}

func (s *server) handleCfgMetricDelete(ctx context.Context, cfg *bond.ConfigNotification) {
	key := cfg.Keys[0]

	if _, ok := s.config.metrics[key]; !ok {
		log.Errorf("Op delete metric, cannot find metric %q", key)
		return
	}
	s.config.metrics[key] = &metricConfig{}
	s.config.metrics[key].Metric.State = adminDisable
	s.deleteMetricTelemetry(ctx, key)
}

func (s *server) handleCfgCustomMetricCreateChange(ctx context.Context, cfg *bond.ConfigNotification) {
	key := cfg.Keys[0]
	newMetricConfig := new(customMetricConfig)
	err := json.Unmarshal([]byte(cfg.Json), newMetricConfig)
	if err != nil {
		log.Errorf("failed to marshal config data from path %s: %v", cfg.PathWithoutKeys, err)
		return
	}
	log.Debugf("read metric config data: %+v", newMetricConfig)

	if _, ok := s.config.customMetric[key]; !ok {
		s.config.customMetric[key] = new(customMetricConfig)
	}

	// store new config
	s.config.customMetric[key] = newMetricConfig
	// update metric telemetry
	s.updateCustomMetricTelemetry(ctx, key, newMetricConfig)
}

func (s *server) handleCfgCustomMetricDelete(ctx context.Context, cfg *bond.ConfigNotification) {
	key := cfg.Keys[0]

	if _, ok := s.config.customMetric[key]; !ok {
		log.Errorf("Op delete custom metric, cannot find custom metric %q", key)
		return
	}
	delete(s.config.customMetric, key)
	s.deleteCustomMetricTelemetry(ctx, key)
}

func (s *server) handleNwInstCfg(ctx context.Context, nwInst *ndk.NetworkInstanceNotification) {
	s.config.m.Lock()
	defer s.config.m.Unlock()

	key := nwInst.GetKey()
	if key == nil {
		return
	}
	name := key.GetInstanceName()
	data := nwInst.GetData()
	log.Debugf("network instance %q op %s base %q oper-up %t", name, nwInst.GetOp(), data.GetBaseName(), data.GetOperIsUp())
	switch nwInst.GetOp() {
	case ndk.SdkMgrOperation_SDK_MGR_OPERATION_CREATE:
		s.config.nwInst[name] = data
		if s.config.baseConfig.NetworkInstance.Value == name {
			if data.GetOperIsUp() &&
				s.config.baseConfig.AdminState == adminEnable &&
				scrapeOn(s.config.baseConfig.Scrape) &&
				s.srv == nil {
				log.Debug("starting server...")
				go s.start(ctx)
			}
		}
	case ndk.SdkMgrOperation_SDK_MGR_OPERATION_UPDATE:
		s.config.nwInst[name] = data
		if s.config.baseConfig.NetworkInstance.Value == name {
			if data == nil || !data.GetOperIsUp() {
				if s.config.baseConfig.OperState == operUp {
					s.shutdown(ctx, time.Second/2)
				}
				return
			}
			if s.config.baseConfig.AdminState == adminEnable &&
				scrapeOn(s.config.baseConfig.Scrape) &&
				s.srv == nil {
				log.Debug("starting server...")
				go s.start(ctx)
			}
		}
	case ndk.SdkMgrOperation_SDK_MGR_OPERATION_DELETE:
		delete(s.config.nwInst, name)
		if s.config.baseConfig.NetworkInstance.Value == name {
			if s.config.baseConfig.OperState == operUp {
				s.shutdown(ctx, time.Second/2)
			}
		}
	default:
		log.Debugf("ignored network instance op %s for %q", nwInst.GetOp(), name)
	}
}

func (s *server) applyDebug(state string) {
	if state == "" {
		return
	}
	enabled := s.config.cliDebug || state == adminEnable || state == "DEBUG_enable" || state == "enable"
	if enabled == s.config.debug {
		return
	}
	s.config.debug = enabled
	SetDebugLogging(enabled)
	if enabled {
		log.Infof("debug logging enabled (config value %q)", state)
		return
	}
	log.Infof("debug logging disabled (config value %q)", state)
}

func (s *server) logKnownNetworkInstances() {
	if !s.config.debug {
		return
	}
	names := make([]string, 0, len(s.config.nwInst))
	for name := range s.config.nwInst {
		names = append(names, name)
	}
	log.Debugf("known network instances: %v", names)
}
