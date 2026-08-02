export type LogFields = Readonly<
  Record<string, string | number | boolean | null | undefined>
>;

export class Logger {
  constructor(private readonly service = "collaboration") {}

  info(message: string, fields: LogFields = {}): void {
    this.write("info", message, fields);
  }

  warn(message: string, fields: LogFields = {}): void {
    this.write("warn", message, fields);
  }

  error(message: string, error: unknown, fields: LogFields = {}): void {
    this.write("error", message, {
      ...fields,
      error_type: error instanceof Error ? error.name : typeof error,
    });
  }

  private write(level: string, message: string, fields: LogFields): void {
    const record: Record<string, string | number | boolean | null> = {
      time: new Date().toISOString(),
      level,
      service: this.service,
      message,
    };
    for (const [key, value] of Object.entries(fields)) {
      if (value !== undefined) record[key] = value;
    }
    process.stdout.write(`${JSON.stringify(record)}\n`);
  }
}
