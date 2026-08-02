export const collaborationCodes = {
  invalidInput: 40_001,
  unauthenticated: 40_002,
  forbidden: 40_003,
  notFound: 40_004,
  conflict: 40_005,
  preconditionFailed: 40_006,
  unavailable: 40_007,
  internal: 40_999,
} as const;

export class ServiceError extends Error {
  constructor(
    readonly status: number,
    readonly code: number,
    readonly key: string,
    readonly detail: string,
    options?: ErrorOptions,
  ) {
    super(detail, options);
    this.name = "ServiceError";
  }
}

export function invalidInput(
  detail = "invalid collaboration input",
  options?: ErrorOptions,
): ServiceError {
  return new ServiceError(
    400,
    collaborationCodes.invalidInput,
    "collaboration.invalid_input",
    detail,
    options,
  );
}

export function unauthenticated(options?: ErrorOptions): ServiceError {
  return new ServiceError(
    401,
    collaborationCodes.unauthenticated,
    "collaboration.unauthenticated",
    "authentication required",
    options,
  );
}

export function forbidden(options?: ErrorOptions): ServiceError {
  return new ServiceError(
    403,
    collaborationCodes.forbidden,
    "collaboration.forbidden",
    "permission denied",
    options,
  );
}

export function notFound(options?: ErrorOptions): ServiceError {
  return new ServiceError(
    404,
    collaborationCodes.notFound,
    "collaboration.not_found",
    "version not found",
    options,
  );
}

export function conflict(
  detail = "resource conflict",
  options?: ErrorOptions,
): ServiceError {
  return new ServiceError(
    409,
    collaborationCodes.conflict,
    "collaboration.conflict",
    detail,
    options,
  );
}

export function preconditionFailed(options?: ErrorOptions): ServiceError {
  return new ServiceError(
    412,
    collaborationCodes.preconditionFailed,
    "collaboration.precondition_failed",
    "document sequence does not match",
    options,
  );
}

export function unavailable(options?: ErrorOptions): ServiceError {
  return new ServiceError(
    503,
    collaborationCodes.unavailable,
    "collaboration.unavailable",
    "dependency unavailable",
    options,
  );
}

export function internal(options?: ErrorOptions): ServiceError {
  return new ServiceError(
    500,
    collaborationCodes.internal,
    "collaboration.internal",
    "internal server error",
    options,
  );
}

export function asServiceError(error: unknown): ServiceError {
  return error instanceof ServiceError ? error : internal({ cause: error });
}
