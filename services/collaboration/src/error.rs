use std::borrow::Cow;

use thiserror::Error;

pub type Result<T> = std::result::Result<T, ServiceError>;

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
#[repr(i32)]
pub enum ErrorCode {
    InvalidInput = 40_001,
    Unauthenticated = 40_002,
    Forbidden = 40_003,
    NotFound = 40_004,
    Conflict = 40_005,
    PreconditionFailed = 40_006,
    Unavailable = 40_007,
    Internal = 40_999,
}

#[derive(Debug, Error)]
#[error("{key}: {detail}")]
pub struct ServiceError {
    code: ErrorCode,
    key: &'static str,
    detail: Cow<'static, str>,
    #[source]
    source: Option<anyhow::Error>,
}

impl ServiceError {
    pub fn new(code: ErrorCode, key: &'static str, detail: impl Into<Cow<'static, str>>) -> Self {
        Self {
            code,
            key,
            detail: detail.into(),
            source: None,
        }
    }

    #[must_use]
    pub fn with_source(mut self, source: impl Into<anyhow::Error>) -> Self {
        self.source = Some(source.into());
        self
    }

    pub const fn code(&self) -> ErrorCode {
        self.code
    }

    pub const fn numeric_code(&self) -> i32 {
        self.code as i32
    }

    pub const fn key(&self) -> &'static str {
        self.key
    }

    pub fn detail(&self) -> &str {
        &self.detail
    }

    pub fn invalid_input(detail: impl Into<Cow<'static, str>>) -> Self {
        Self::new(
            ErrorCode::InvalidInput,
            "collaboration.invalid_input",
            detail,
        )
    }

    pub fn unauthenticated() -> Self {
        Self::new(
            ErrorCode::Unauthenticated,
            "collaboration.unauthenticated",
            "authentication required",
        )
    }

    pub fn forbidden() -> Self {
        Self::new(
            ErrorCode::Forbidden,
            "collaboration.forbidden",
            "permission denied",
        )
    }

    pub fn not_found(detail: impl Into<Cow<'static, str>>) -> Self {
        Self::new(ErrorCode::NotFound, "collaboration.not_found", detail)
    }

    pub fn conflict(detail: impl Into<Cow<'static, str>>) -> Self {
        Self::new(ErrorCode::Conflict, "collaboration.conflict", detail)
    }

    pub fn precondition_failed() -> Self {
        Self::new(
            ErrorCode::PreconditionFailed,
            "collaboration.precondition_failed",
            "document sequence does not match",
        )
    }

    pub fn unavailable(source: impl Into<anyhow::Error>) -> Self {
        Self::new(
            ErrorCode::Unavailable,
            "collaboration.unavailable",
            "dependency unavailable",
        )
        .with_source(source)
    }

    pub fn internal(source: impl Into<anyhow::Error>) -> Self {
        Self::new(
            ErrorCode::Internal,
            "collaboration.internal",
            "internal server error",
        )
        .with_source(source)
    }
}
