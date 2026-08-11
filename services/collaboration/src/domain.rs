use std::{fmt, str::FromStr, sync::Arc, time::Instant};

use serde::{Deserialize, Serialize};
use time::OffsetDateTime;
use uuid::{Uuid, Version};

use crate::error::{Result, ServiceError};

#[derive(Clone, Copy, Debug, Eq, Hash, Ord, PartialEq, PartialOrd, Serialize, Deserialize)]
#[serde(transparent)]
pub struct DocumentId(Uuid);

impl DocumentId {
    pub fn new() -> Self {
        Self(Uuid::now_v7())
    }

    /// Parses a `UUIDv7` document identifier.
    ///
    /// # Errors
    ///
    /// Returns an invalid-input error when `value` is not a canonical `UUIDv7`.
    pub fn parse(value: &str) -> Result<Self> {
        let value = value.trim();
        let id = Uuid::parse_str(value).map_err(|error| {
            ServiceError::invalid_input("document_id must be a UUIDv7").with_source(error)
        })?;
        if id.get_version() != Some(Version::SortRand) {
            return Err(ServiceError::invalid_input("document_id must be a UUIDv7"));
        }
        Ok(Self(id))
    }

    pub const fn as_uuid(self) -> Uuid {
        self.0
    }
}

impl Default for DocumentId {
    fn default() -> Self {
        Self::new()
    }
}

impl fmt::Display for DocumentId {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        self.0.fmt(formatter)
    }
}

impl FromStr for DocumentId {
    type Err = ServiceError;

    fn from_str(value: &str) -> Result<Self> {
        Self::parse(value)
    }
}

#[derive(Clone, Copy, Debug, Eq, Hash, Ord, PartialEq, PartialOrd, Serialize, Deserialize)]
#[serde(transparent)]
pub struct VersionId(Uuid);

impl VersionId {
    pub fn new() -> Self {
        Self(Uuid::now_v7())
    }

    /// Parses a `UUIDv7` version identifier.
    ///
    /// # Errors
    ///
    /// Returns an invalid-input error when `value` is not a canonical `UUIDv7`.
    pub fn parse(value: &str) -> Result<Self> {
        let value = value.trim();
        let id = Uuid::parse_str(value).map_err(|error| {
            ServiceError::invalid_input("version_id must be a UUIDv7").with_source(error)
        })?;
        if id.get_version() != Some(Version::SortRand) {
            return Err(ServiceError::invalid_input("version_id must be a UUIDv7"));
        }
        Ok(Self(id))
    }

    pub const fn as_uuid(self) -> Uuid {
        self.0
    }
}

impl Default for VersionId {
    fn default() -> Self {
        Self::new()
    }
}

impl fmt::Display for VersionId {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        self.0.fmt(formatter)
    }
}

impl FromStr for VersionId {
    type Err = ServiceError;

    fn from_str(value: &str) -> Result<Self> {
        Self::parse(value)
    }
}

#[derive(Clone, Copy, Debug, Deserialize, Eq, PartialEq, Serialize)]
#[serde(rename_all = "lowercase")]
pub enum Access {
    Viewer,
    Editor,
    Owner,
}

impl Access {
    pub const fn can_write(self) -> bool {
        matches!(self, Self::Editor | Self::Owner)
    }
}

impl FromStr for Access {
    type Err = ServiceError;

    fn from_str(value: &str) -> Result<Self> {
        match value {
            "viewer" => Ok(Self::Viewer),
            "editor" => Ok(Self::Editor),
            "owner" => Ok(Self::Owner),
            _ => Err(ServiceError::invalid_input("access is invalid")),
        }
    }
}

impl fmt::Display for Access {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        formatter.write_str(match self {
            Self::Viewer => "viewer",
            Self::Editor => "editor",
            Self::Owner => "owner",
        })
    }
}

#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
pub struct PublicUser {
    pub id: i64,
    pub username: String,
    pub avatar: String,
}

impl PublicUser {
    /// Validates the authenticated actor snapshot stored with a change.
    ///
    /// # Errors
    ///
    /// Returns an invalid-input error when an actor field violates its boundary.
    pub fn validate(&self) -> Result<()> {
        if self.id <= 0
            || self.username.trim().is_empty()
            || self.username.chars().count() > 32
            || self.avatar.len() > 4096
        {
            return Err(ServiceError::invalid_input("authenticated user is invalid"));
        }
        Ok(())
    }
}

#[derive(Clone, Debug, Eq, PartialEq)]
pub struct Authorization {
    pub document_id: DocumentId,
    pub actor: PublicUser,
    pub access: Access,
    pub permission_revision: i64,
    pub token_expires_at: OffsetDateTime,
}

#[derive(Clone)]
pub struct Secret(Arc<str>);

impl Secret {
    /// Wraps a non-empty, bounded secret without exposing it through `Debug`.
    ///
    /// # Errors
    ///
    /// Returns an unauthenticated error for empty, padded, or oversized values.
    pub fn new(value: impl Into<String>) -> Result<Self> {
        let value = value.into();
        if value.is_empty() || value.len() > 16_384 || value.trim() != value {
            return Err(ServiceError::unauthenticated());
        }
        Ok(Self(Arc::from(value)))
    }

    pub fn expose(&self) -> &str {
        &self.0
    }
}

impl fmt::Debug for Secret {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        formatter.write_str("[REDACTED]")
    }
}

#[derive(Clone, Debug)]
pub struct RequestContext {
    pub request_id: Arc<str>,
    pub access_token: Option<Secret>,
    pub deadline: Option<Instant>,
    pub trace_parent: Option<Arc<str>>,
    pub trace_state: Option<Arc<str>>,
    pub baggage: Option<Arc<str>>,
}

impl RequestContext {
    pub fn new(request_id: impl Into<Arc<str>>) -> Self {
        Self {
            request_id: request_id.into(),
            access_token: None,
            deadline: None,
            trace_parent: None,
            trace_state: None,
            baggage: None,
        }
    }

    pub fn propagation_headers(&self) -> std::collections::BTreeMap<String, String> {
        let mut headers = std::collections::BTreeMap::new();
        headers.insert("X-Request-ID".to_owned(), self.request_id.to_string());
        headers.insert("X-Correlation-ID".to_owned(), self.request_id.to_string());
        if let Some(value) = &self.trace_parent {
            headers.insert("traceparent".to_owned(), value.to_string());
        }
        if let Some(value) = &self.trace_state {
            headers.insert("tracestate".to_owned(), value.to_string());
        }
        if let Some(value) = &self.baggage
            && value.len() <= 8_192
        {
            headers.insert("baggage".to_owned(), value.to_string());
        }
        headers
    }

    #[must_use]
    pub fn with_deadline(&self, maximum_wait: std::time::Duration) -> Self {
        let mut context = self.clone();
        context.deadline = std::time::Instant::now().checked_add(maximum_wait);
        context
    }
}

#[derive(Clone, Copy, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(rename_all = "lowercase")]
pub enum VersionKind {
    Manual,
    Automatic,
    Restoration,
}

impl VersionKind {
    pub const fn as_str(self) -> &'static str {
        match self {
            Self::Manual => "manual",
            Self::Automatic => "automatic",
            Self::Restoration => "restoration",
        }
    }
}

impl FromStr for VersionKind {
    type Err = ServiceError;

    fn from_str(value: &str) -> Result<Self> {
        match value {
            "manual" => Ok(Self::Manual),
            "automatic" => Ok(Self::Automatic),
            "restoration" => Ok(Self::Restoration),
            _ => Err(ServiceError::internal(anyhow::anyhow!(
                "stored version kind is invalid"
            ))),
        }
    }
}

#[derive(Clone, Debug)]
pub struct DocumentVersion {
    pub id: VersionId,
    pub document_id: DocumentId,
    pub sequence: i64,
    pub kind: VersionKind,
    pub label: Option<String>,
    pub state: Vec<u8>,
    pub created_by: PublicUser,
    pub created_at: OffsetDateTime,
}

#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
pub struct Projection {
    pub content: serde_json::Value,
    pub plain_text: String,
}
