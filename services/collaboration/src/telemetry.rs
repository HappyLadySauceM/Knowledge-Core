use std::{sync::Arc, time::Duration};

use opentelemetry::{global, propagation::TextMapCompositePropagator, trace::TracerProvider as _};
use opentelemetry_otlp::WithExportConfig as _;
use opentelemetry_sdk::{
    Resource,
    propagation::{BaggagePropagator, TraceContextPropagator},
    trace::SdkTracerProvider,
};
use prometheus::{
    Encoder, HistogramOpts, HistogramVec, IntCounterVec, IntGauge, Opts, Registry, TextEncoder,
};
use tracing_subscriber::{
    EnvFilter, Registry as SubscriberRegistry, layer::SubscriberExt as _, reload,
    util::SubscriberInitExt as _,
};

use crate::{
    SERVICE_NAME,
    actor::CloseSignal,
    config::{Environment, TelemetryConfig},
    error::{Result, ServiceError},
};

const NACOS_SDK_LOG_GUARD: &str = "nacos_sdk=warn,nacos_sdk::api::plugin::auth::auth_by_http=off";

#[derive(Clone)]
pub struct Metrics {
    inner: Arc<MetricSet>,
}

struct MetricSet {
    registry: Registry,
    active_actors: IntGauge,
    active_connections: IntGauge,
    websocket_handshakes: IntCounterVec,
    websocket_closes: IntCounterVec,
    update_duration: HistogramVec,
    worker_operations: IntCounterVec,
    config_connected: IntGauge,
    config_last_success: IntGauge,
    config_reloads: IntCounterVec,
}

impl Metrics {
    /// Registers the complete bounded-cardinality Collaboration metric set.
    ///
    /// # Errors
    ///
    /// Returns an error when a metric descriptor or collector cannot be registered.
    pub fn new() -> Result<Self> {
        let registry = Registry::new_custom(Some("knowledge_core_collaboration".to_owned()), None)
            .map_err(metric_error)?;
        let active_actors = IntGauge::with_opts(Opts::new(
            "active_document_actors",
            "Number of currently active document actors.",
        ))
        .map_err(metric_error)?;
        let active_connections = IntGauge::with_opts(Opts::new(
            "active_websocket_connections",
            "Number of currently active collaboration WebSocket connections.",
        ))
        .map_err(metric_error)?;
        let websocket_handshakes = IntCounterVec::new(
            Opts::new(
                "websocket_handshakes_total",
                "Collaboration WebSocket handshake outcomes.",
            ),
            &["status"],
        )
        .map_err(metric_error)?;
        let websocket_closes = IntCounterVec::new(
            Opts::new(
                "websocket_closes_total",
                "Collaboration WebSocket close outcomes.",
            ),
            &["reason"],
        )
        .map_err(metric_error)?;
        let update_duration = HistogramVec::new(
            HistogramOpts::new(
                "update_commit_duration_seconds",
                "Time spent durably committing collaboration updates.",
            )
            .buckets(vec![
                0.001, 0.0025, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1.0, 2.5, 5.0,
            ]),
            &["outcome"],
        )
        .map_err(metric_error)?;
        let worker_operations = IntCounterVec::new(
            Opts::new(
                "worker_operations_total",
                "Collaboration worker operation outcomes.",
            ),
            &["operation", "outcome"],
        )
        .map_err(metric_error)?;
        let config_connected = IntGauge::with_opts(Opts::new(
            "nacos_config_connected",
            "Whether the latest Nacos configuration operation succeeded.",
        ))
        .map_err(metric_error)?;
        let config_last_success = IntGauge::with_opts(Opts::new(
            "nacos_config_last_success_unixtime",
            "Unix timestamp of the latest valid Nacos configuration response.",
        ))
        .map_err(metric_error)?;
        let config_reloads = IntCounterVec::new(
            Opts::new(
                "config_reloads_total",
                "Dynamic configuration reload outcomes.",
            ),
            &["outcome"],
        )
        .map_err(metric_error)?;

        for collector in [
            Box::new(active_actors.clone()) as Box<dyn prometheus::core::Collector>,
            Box::new(active_connections.clone()),
            Box::new(websocket_handshakes.clone()),
            Box::new(websocket_closes.clone()),
            Box::new(update_duration.clone()),
            Box::new(worker_operations.clone()),
            Box::new(config_connected.clone()),
            Box::new(config_last_success.clone()),
            Box::new(config_reloads.clone()),
        ] {
            registry.register(collector).map_err(metric_error)?;
        }

        Ok(Self {
            inner: Arc::new(MetricSet {
                registry,
                active_actors,
                active_connections,
                websocket_handshakes,
                websocket_closes,
                update_duration,
                worker_operations,
                config_connected,
                config_last_success,
                config_reloads,
            }),
        })
    }

    pub fn actor_started(&self) {
        self.inner.active_actors.inc();
    }

    pub fn actor_stopped(&self) {
        self.inner.active_actors.dec();
    }

    pub fn connection_opened(&self) {
        self.inner.active_connections.inc();
    }

    pub fn connection_closed(&self, reason: &'static str) {
        self.inner.active_connections.dec();
        self.inner
            .websocket_closes
            .with_label_values(&[reason])
            .inc();
    }

    pub fn connections_removed(&self, count: usize) {
        if let Ok(count) = i64::try_from(count) {
            self.inner.active_connections.sub(count);
        }
    }

    pub fn handshake(&self, status: &'static str) {
        self.inner
            .websocket_handshakes
            .with_label_values(&[status])
            .inc();
    }

    pub fn observe_update(&self, elapsed: Duration, error: Option<CloseSignal>) {
        let outcome = error.map_or("committed", |close| close.reason);
        self.inner
            .update_duration
            .with_label_values(&[outcome])
            .observe(elapsed.as_secs_f64());
    }

    pub fn worker_operation(&self, operation: &'static str, succeeded: bool) {
        self.inner
            .worker_operations
            .with_label_values(&[operation, if succeeded { "ok" } else { "error" }])
            .inc();
    }

    pub(crate) fn config_success(&self) {
        self.inner.config_connected.set(1);
        self.inner
            .config_last_success
            .set(time::OffsetDateTime::now_utc().unix_timestamp());
    }

    pub(crate) fn config_failure(&self) {
        self.inner.config_connected.set(0);
    }

    pub(crate) fn config_applied(&self) {
        self.config_success();
        self.inner
            .config_reloads
            .with_label_values(&["applied"])
            .inc();
    }

    pub(crate) fn config_rejected(&self) {
        self.inner
            .config_reloads
            .with_label_values(&["rejected"])
            .inc();
    }

    /// Encodes the current registry in the Prometheus text exposition format.
    ///
    /// # Errors
    ///
    /// Returns an error when the registry cannot be encoded.
    pub fn encode(&self) -> Result<Vec<u8>> {
        let mut output = Vec::new();
        TextEncoder::new()
            .encode(&self.inner.registry.gather(), &mut output)
            .map_err(metric_error)?;
        Ok(output)
    }
}

pub struct Telemetry {
    provider: Option<SdkTracerProvider>,
    log_controller: LogController,
    shutdown_timeout: Duration,
}

#[derive(Clone)]
pub(crate) struct LogController {
    handle: reload::Handle<EnvFilter, SubscriberRegistry>,
    environment: Environment,
}

impl LogController {
    pub(crate) fn set_level(&self, level: &str) -> Result<()> {
        let filter = log_filter(self.environment, level)?;
        self.handle.reload(filter).map_err(|error| {
            ServiceError::internal(anyhow::anyhow!(error).context("reload log filter"))
        })
    }
}

impl Telemetry {
    /// Installs structured logging, W3C propagation, and optional OTLP tracing.
    ///
    /// # Errors
    ///
    /// Returns an error when the exporter or global tracing subscriber cannot be initialized.
    pub fn initialize(environment: Environment, config: &TelemetryConfig) -> Result<Self> {
        let filter = log_filter(environment, &config.log_level)?;
        let (filter_layer, filter_handle) = reload::Layer::new(filter);
        let provider = config
            .otlp_endpoint
            .as_ref()
            .map(|endpoint| build_provider(endpoint.as_str(), config.export_timeout))
            .transpose()?;
        let otlp_layer = provider.as_ref().map(|provider| {
            tracing_opentelemetry::layer().with_tracer(provider.tracer(SERVICE_NAME))
        });
        tracing_subscriber::registry()
            .with(filter_layer)
            .with(tracing_subscriber::fmt::layer().json().flatten_event(true))
            .with(otlp_layer)
            .try_init()
            .map_err(|error| {
                ServiceError::internal(anyhow::anyhow!(error).context("initialize telemetry"))
            })?;
        global::set_text_map_propagator(TextMapCompositePropagator::new(vec![
            Box::new(TraceContextPropagator::new()),
            Box::new(BaggagePropagator::new()),
        ]));
        if let Some(provider) = &provider {
            global::set_tracer_provider(provider.clone());
        }
        Ok(Self {
            provider,
            log_controller: LogController {
                handle: filter_handle,
                environment,
            },
            shutdown_timeout: config.shutdown_timeout,
        })
    }

    pub(crate) fn log_controller(&self) -> LogController {
        self.log_controller.clone()
    }

    /// Flushes telemetry using the configured shutdown timeout.
    ///
    /// # Errors
    ///
    /// Returns an error when the trace provider cannot flush before the deadline.
    pub fn shutdown(self) -> Result<()> {
        let maximum_wait = self.shutdown_timeout;
        self.shutdown_with_timeout(maximum_wait)
    }

    /// Flushes telemetry using the smaller configured or process-level timeout.
    ///
    /// # Errors
    ///
    /// Returns an error when the trace provider cannot flush before the deadline.
    pub fn shutdown_with_timeout(self, maximum_wait: Duration) -> Result<()> {
        let Some(provider) = self.provider else {
            return Ok(());
        };
        provider
            .shutdown_with_timeout(self.shutdown_timeout.min(maximum_wait))
            .map_err(|error| {
                ServiceError::internal(
                    anyhow::anyhow!(error).context("flush OpenTelemetry provider"),
                )
            })
    }
}

fn log_filter(environment: Environment, level: &str) -> Result<EnvFilter> {
    crate::remote_config::validate_log_level(level)?;
    let directive = match environment {
        Environment::Development | Environment::Test => {
            format!("{level},{NACOS_SDK_LOG_GUARD}")
        }
        Environment::Production => {
            format!("{level},{NACOS_SDK_LOG_GUARD},sqlx=warn,hyper=warn")
        }
    };
    EnvFilter::try_new(directive).map_err(|error| {
        ServiceError::invalid_input("log level produced an invalid tracing filter")
            .with_source(error)
    })
}

fn build_provider(endpoint: &str, export_timeout: Duration) -> Result<SdkTracerProvider> {
    let exporter = opentelemetry_otlp::SpanExporter::builder()
        .with_tonic()
        .with_endpoint(endpoint)
        .with_timeout(export_timeout)
        .build()
        .map_err(|error| {
            ServiceError::internal(anyhow::anyhow!(error).context("build OTLP trace exporter"))
        })?;
    let resource = Resource::builder().with_service_name(SERVICE_NAME).build();
    Ok(SdkTracerProvider::builder()
        .with_resource(resource)
        .with_batch_exporter(exporter)
        .build())
}

fn metric_error(error: prometheus::Error) -> ServiceError {
    ServiceError::internal(anyhow::anyhow!(error).context("initialize Prometheus metric"))
}

#[cfg(test)]
mod tests {
    use super::log_filter;
    use crate::config::Environment;

    #[test]
    fn nacos_dependency_cannot_emit_credentials_or_configuration_at_debug_level() {
        let filter = log_filter(Environment::Development, "debug").expect("valid filter");
        let rendered = filter.to_string();
        assert!(rendered.contains("nacos_sdk=warn"));
        assert!(rendered.contains("nacos_sdk::api::plugin::auth::auth_by_http=off"));
    }
}
