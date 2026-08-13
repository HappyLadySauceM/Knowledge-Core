package configcenter

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/nacos-group/nacos-sdk-go/v2/clients"
	configclient "github.com/nacos-group/nacos-sdk-go/v2/clients/config_client"
	"github.com/nacos-group/nacos-sdk-go/v2/common/constant"
	"github.com/nacos-group/nacos-sdk-go/v2/vo"
	"github.com/prometheus/client_golang/prometheus"
)

type CleanupRegistrar func(string, func(context.Context) error) error

type Manager struct {
	bootstrap Bootstrap
	factory   func(Bootstrap) (configclient.IConfigClient, error)

	current atomic.Pointer[DynamicDocument]
	mu      sync.Mutex
	client  configclient.IConfigClient
	cancel  context.CancelFunc
	done    chan struct{}
	close   sync.Once
	logger  *slog.Logger
	metrics managerMetrics
	apply   func(DynamicDocument) (ApplyResult, error)
}

type managerMetrics struct {
	connected       prometheus.Gauge
	lastSuccess     prometheus.Gauge
	reloads         *prometheus.CounterVec
	restartRequired prometheus.Gauge
}

type ApplyResult struct {
	RestartRequiredFields []string
}

func NewManager(bootstrap Bootstrap) *Manager {
	return &Manager{bootstrap: bootstrap, factory: newNacosClient}
}

func Publish(ctx context.Context, bootstrap Bootstrap, encrypted []byte) error {
	if ctx == nil {
		return errors.New("publish Nacos configuration: context is required")
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("publish Nacos configuration: %w", err)
	}
	if !bootstrap.Enabled {
		return errors.New("publish Nacos configuration: Nacos must be enabled")
	}
	plaintext, err := Decrypt(encrypted, bootstrap.Key, bootstrap.KeyID, bootstrap.Binding)
	if err != nil {
		return fmt.Errorf("publish Nacos configuration: validate envelope: %w", err)
	}
	if _, err := DecodeDynamicDocument(plaintext); err != nil {
		return fmt.Errorf("publish Nacos configuration: validate document: %w", err)
	}
	client, err := newNacosClient(bootstrap)
	if err != nil {
		return fmt.Errorf("publish Nacos configuration: create client: %w", err)
	}
	defer client.CloseClient()
	published, err := client.PublishConfig(vo.ConfigParam{
		DataId:  bootstrap.Binding.DataID,
		Group:   bootstrap.Binding.Group,
		Content: string(encrypted),
		Type:    "json",
	})
	if err != nil {
		return fmt.Errorf("publish Nacos configuration: %w", err)
	}
	if !published {
		return errors.New("publish Nacos configuration: server rejected the update")
	}
	return nil
}

func (m *Manager) Enabled() bool {
	return m != nil && m.bootstrap.Enabled
}

func (m *Manager) Load(ctx context.Context) (*DynamicDocument, error) {
	if !m.Enabled() {
		return nil, nil
	}
	if ctx == nil {
		return nil, errors.New("load Nacos configuration: context is required")
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("load Nacos configuration: %w", err)
	}
	client, err := m.factory(m.bootstrap)
	if err != nil {
		return nil, fmt.Errorf("load Nacos configuration: create client: %w", err)
	}
	defer client.CloseClient()
	document, err := m.fetch(client)
	if err != nil {
		return nil, fmt.Errorf("load Nacos configuration: %w", err)
	}
	m.current.Store(&document)
	return &document, nil
}

func (m *Manager) Start(
	ctx context.Context,
	logger *slog.Logger,
	registerer prometheus.Registerer,
	apply func(DynamicDocument) (ApplyResult, error),
	registerCleanup CleanupRegistrar,
) error {
	if !m.Enabled() {
		return nil
	}
	if ctx == nil || logger == nil || registerer == nil || apply == nil || registerCleanup == nil {
		return errors.New("start Nacos configuration: runtime dependencies are required")
	}
	metrics, err := newManagerMetrics(registerer)
	if err != nil {
		return err
	}
	client, err := m.factory(m.bootstrap)
	if err != nil {
		return fmt.Errorf("start Nacos configuration: create client: %w", err)
	}
	m.logger = logger
	m.metrics = metrics
	m.apply = apply
	m.client = client

	document, err := m.fetch(client)
	if err != nil {
		client.CloseClient()
		m.client = nil
		return fmt.Errorf("start Nacos configuration: %w", err)
	}
	if err := m.applyDocument(document); err != nil {
		client.CloseClient()
		m.client = nil
		return fmt.Errorf("start Nacos configuration: apply initial document: %w", err)
	}
	listener := vo.ConfigParam{
		DataId: m.bootstrap.Binding.DataID,
		Group:  m.bootstrap.Binding.Group,
		OnChange: func(namespace, group, dataID, contents string) {
			m.handleUpdate(namespace, group, dataID, contents)
		},
	}
	if err := client.ListenConfig(listener); err != nil {
		client.CloseClient()
		m.client = nil
		return fmt.Errorf("start Nacos configuration listener: %w", err)
	}
	workerCtx, cancel := context.WithCancel(ctx)
	m.cancel = cancel
	m.done = make(chan struct{})
	go m.poll(workerCtx)
	if err := registerCleanup("nacos-config", m.Close); err != nil {
		_ = m.Close(context.Background())
		return fmt.Errorf("start Nacos configuration: register cleanup: %w", err)
	}
	logger.InfoContext(ctx, "dynamic configuration listener started",
		slog.String("component", "config.nacos"),
		slog.String("event", "listener_started"),
		slog.String("data_id", m.bootstrap.Binding.DataID),
	)
	return nil
}

func (m *Manager) Close(ctx context.Context) error {
	if m == nil {
		return nil
	}
	var closeErr error
	m.close.Do(func() {
		if m.cancel != nil {
			m.cancel()
		}
		if m.done != nil {
			if ctx == nil {
				ctx = context.Background()
			}
			select {
			case <-m.done:
			case <-ctx.Done():
				closeErr = fmt.Errorf("wait for Nacos configuration worker: %w", ctx.Err())
				return
			}
		}
		if m.client != nil {
			if err := m.client.CancelListenConfig(vo.ConfigParam{
				DataId: m.bootstrap.Binding.DataID,
				Group:  m.bootstrap.Binding.Group,
			}); err != nil {
				closeErr = fmt.Errorf("stop Nacos configuration listener: %w", err)
			}
			m.client.CloseClient()
		}
	})
	return closeErr
}

func (m *Manager) fetch(client configclient.IConfigClient) (DynamicDocument, error) {
	contents, err := client.GetConfig(vo.ConfigParam{
		DataId: m.bootstrap.Binding.DataID,
		Group:  m.bootstrap.Binding.Group,
	})
	if err != nil {
		return DynamicDocument{}, fmt.Errorf("fetch encrypted document: %w", err)
	}
	return m.decode(contents)
}

func (m *Manager) decode(contents string) (DynamicDocument, error) {
	plaintext, err := Decrypt(
		[]byte(contents),
		m.bootstrap.Key,
		m.bootstrap.KeyID,
		m.bootstrap.Binding,
	)
	if err != nil {
		return DynamicDocument{}, err
	}
	return DecodeDynamicDocument(plaintext)
}

func (m *Manager) handleUpdate(namespace, group, dataID, contents string) {
	if namespace != m.bootstrap.Binding.Namespace || group != m.bootstrap.Binding.Group || dataID != m.bootstrap.Binding.DataID {
		m.reject(errors.New("configuration callback binding does not match the requested document"))
		return
	}
	document, err := m.decode(contents)
	if err != nil {
		m.reject(err)
		return
	}
	if err := m.applyDocument(document); err != nil {
		m.reject(err)
	}
}

func (m *Manager) applyDocument(document DynamicDocument) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	current := m.current.Load()
	if current != nil {
		if document.Revision < current.Revision {
			return fmt.Errorf("reject dynamic configuration revision rollback from %d to %d", current.Revision, document.Revision)
		}
		if document.Revision == current.Revision {
			if !document.Equal(*current) {
				return fmt.Errorf("reject conflicting dynamic configuration revision %d", document.Revision)
			}
			m.markSuccess()
			return nil
		}
	}
	result, err := m.apply(document)
	if err != nil {
		return err
	}
	copy := document
	m.current.Store(&copy)
	m.markSuccess()
	if m.metrics.reloads != nil {
		outcome := "applied"
		if len(result.RestartRequiredFields) != 0 {
			outcome = "applied_restart_required"
		}
		m.metrics.reloads.WithLabelValues(outcome).Inc()
		if len(result.RestartRequiredFields) == 0 {
			m.metrics.restartRequired.Set(0)
		} else {
			m.metrics.restartRequired.Set(1)
		}
	}
	if m.logger != nil {
		m.logger.Info("dynamic configuration applied",
			slog.String("component", "config.nacos"),
			slog.String("event", "reload"),
			slog.Uint64("revision", document.Revision),
			slog.Any("restart_required_fields", result.RestartRequiredFields),
		)
	}
	return nil
}

func (m *Manager) reject(err error) {
	if m.metrics.reloads != nil {
		m.metrics.reloads.WithLabelValues("rejected").Inc()
	}
	if m.logger != nil {
		m.logger.Error("dynamic configuration update rejected",
			slog.String("component", "config.nacos"),
			slog.String("event", "reload_rejected"),
			slog.Any("error", err),
		)
	}
}

func (m *Manager) poll(ctx context.Context) {
	defer close(m.done)
	ticker := time.NewTicker(m.bootstrap.PollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			document, err := m.fetch(m.client)
			if err != nil {
				m.metrics.connected.Set(0)
				m.logger.ErrorContext(ctx, "dynamic configuration health check failed; retaining last-good configuration",
					slog.String("component", "config.nacos"),
					slog.String("event", "dependency_error"),
					slog.Any("error", err),
				)
				continue
			}
			if err := m.applyDocument(document); err != nil {
				m.reject(err)
			}
		}
	}
}

func (m *Manager) markSuccess() {
	if m.metrics.connected != nil {
		m.metrics.connected.Set(1)
		m.metrics.lastSuccess.SetToCurrentTime()
	}
}

func newManagerMetrics(registerer prometheus.Registerer) (managerMetrics, error) {
	metrics := managerMetrics{
		connected: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: "knowledge_core",
			Subsystem: "config",
			Name:      "nacos_connected",
			Help:      "Whether the latest Nacos configuration operation succeeded.",
		}),
		lastSuccess: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: "knowledge_core",
			Subsystem: "config",
			Name:      "nacos_last_success_unixtime",
			Help:      "Unix timestamp of the latest valid Nacos configuration response.",
		}),
		reloads: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "knowledge_core",
			Subsystem: "config",
			Name:      "reloads_total",
			Help:      "Dynamic configuration reload outcomes.",
		}, []string{"outcome"}),
		restartRequired: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: "knowledge_core",
			Subsystem: "config",
			Name:      "restart_required",
			Help:      "Whether the latest applied configuration contains startup-only changes.",
		}),
	}
	collectors := []prometheus.Collector{metrics.connected, metrics.lastSuccess, metrics.reloads, metrics.restartRequired}
	for index, collector := range collectors {
		if err := registerer.Register(collector); err != nil {
			for _, registered := range collectors[:index] {
				registerer.Unregister(registered)
			}
			return managerMetrics{}, fmt.Errorf("register Nacos configuration metrics: %w", err)
		}
	}
	return metrics, nil
}

func newNacosClient(bootstrap Bootstrap) (configclient.IConfigClient, error) {
	if err := os.MkdirAll(filepath.Join(bootstrap.RuntimeDir, "cache"), 0o700); err != nil {
		return nil, fmt.Errorf("create Nacos cache directory: %w", err)
	}
	if err := os.MkdirAll(filepath.Join(bootstrap.RuntimeDir, "log"), 0o700); err != nil {
		return nil, fmt.Errorf("create Nacos log directory: %w", err)
	}
	serverConfigs := make([]constant.ServerConfig, 0, len(bootstrap.Endpoints))
	for _, endpoint := range bootstrap.Endpoints {
		options := []constant.ServerOption{constant.WithScheme(endpoint.Scheme)}
		serverConfigs = append(serverConfigs, *constant.NewServerConfig(endpoint.Host, endpoint.Port, options...))
	}
	clientConfig := *constant.NewClientConfig(
		constant.WithAppName(bootstrap.Service),
		constant.WithNamespaceId(bootstrap.Binding.Namespace),
		constant.WithUsername(bootstrap.Username),
		constant.WithPassword(bootstrap.Password),
		constant.WithTimeoutMs(uint64(bootstrap.Timeout.Milliseconds())),
		constant.WithNotLoadCacheAtStart(true),
		constant.WithDisableUseSnapShot(true),
		constant.WithCacheDir(filepath.Join(bootstrap.RuntimeDir, "cache")),
		constant.WithLogDir(filepath.Join(bootstrap.RuntimeDir, "log")),
		constant.WithLogLevel("warn"),
		constant.WithTLS(*constant.NewTLSConfig(constant.WithCA(bootstrap.CAFile, ""))),
	)
	client, err := clients.CreateConfigClient(map[string]interface{}{
		constant.KEY_SERVER_CONFIGS: serverConfigs,
		constant.KEY_CLIENT_CONFIG:  clientConfig,
	})
	if err != nil {
		return nil, fmt.Errorf("initialize Nacos SDK: %w", err)
	}
	return client, nil
}
