import { Runtime } from "./runtime.js";

async function main(): Promise<void> {
  const runtime = await Runtime.start();
  const signal = await waitForSignal();
  runtime.logger.info("Collaboration shutdown requested", {
    component: "collaboration.runtime",
    event: "shutdown_requested",
    signal,
  });
  await withTimeout(runtime.shutdown(), runtime.config.shutdownTimeoutMs);
}

function waitForSignal(): Promise<NodeJS.Signals> {
  return new Promise((resolve) => {
    const finish = (signal: NodeJS.Signals): void => {
      process.off("SIGINT", finish);
      process.off("SIGTERM", finish);
      resolve(signal);
    };
    process.once("SIGINT", finish);
    process.once("SIGTERM", finish);
  });
}

async function withTimeout(
  operation: Promise<void>,
  milliseconds: number,
): Promise<void> {
  let timer: NodeJS.Timeout | undefined;
  const timeout = new Promise<never>((_resolve, reject) => {
    timer = setTimeout(
      () =>
        reject(
          new Error(
            `Collaboration shutdown exceeded ${String(milliseconds)}ms`,
          ),
        ),
      milliseconds,
    );
  });
  try {
    await Promise.race([operation, timeout]);
  } finally {
    if (timer !== undefined) clearTimeout(timer);
  }
}

void main().catch((error: unknown) => {
  const record = {
    time: new Date().toISOString(),
    level: "error",
    service: "collaboration",
    message: "Collaboration process failed",
    error_type: error instanceof Error ? error.name : typeof error,
  };
  process.stderr.write(`${JSON.stringify(record)}\n`);
  process.exitCode = 1;
});
