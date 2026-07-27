# Database migrations

All schema changes live under the service that owns the data and the database
provider that executes them:

```text
migrations/<service>/<provider>/<version>_<name>.up.sql
migrations/<service>/<provider>/<version>_<name>.down.sql
```

PostgreSQL migration chains live under:

- `migrations/identity/postgres/` (Identity users migration implemented)
- `migrations/knowledge/postgres/`
- `migrations/platform/postgres/`

Each owner service embeds its provider-specific SQL files and automatically
applies all up migrations before repositories, consumers, or network listeners
start. A migration error or dirty version prevents the service from becoming
ready. The database driver's migration lock serializes concurrent replicas.
