# Database migrations

All schema changes live under the service that owns the data and the database
provider that executes them:

```text
migrations/<service>/<provider>/<version>_<name>.up.sql
migrations/<service>/<provider>/<version>_<name>.down.sql
```

The initial PostgreSQL chains will be created under:

- `migrations/identity/postgres/`
- `migrations/knowledge/postgres/`
- `migrations/platform/postgres/`

Services do not run migrations during startup. A later migration command or
the deployment pipeline must execute only the current service/provider chain.
