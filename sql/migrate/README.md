# PostgreSQL Migration

Migration CLI lives in [`main.go`](main.go). Entry scripts: [`migrate.ps1`](../migrate.ps1) on Windows and [`migrate.sh`](../migrate.sh) on Unix.

## Common Commands

Start local PostgreSQL first:

```powershell
docker compose up -d postgres
```

For the full API runtime, start Redis too:

```powershell
docker compose up -d postgres redis
```

```powershell
.\sql\migrate.ps1
.\sql\migrate.ps1 -DatabaseUrl 'postgres://knowledge_core:knowledge_core@localhost:5432/knowledge_core?sslmode=disable'
.\sql\migrate.ps1 -Force
```

```bash
./sql/migrate.sh
KNOWLEDGE_CORE_DATABASE_URL='postgres://knowledge_core:knowledge_core@localhost:5432/knowledge_core?sslmode=disable' ./sql/migrate.sh
MIGRATION_FORCE=1 ./sql/migrate.sh
```

## Behavior

The migrator connects to the `postgres` maintenance database first and creates the target database from `KNOWLEDGE_CORE_DATABASE_URL` when it does not exist yet. It then scans top-level numeric `.sql` files under `sql/migrations`, applies them in lexical order, and records `version`, `checksum`, and `applied_at` in `schema_migrations`. A PostgreSQL advisory lock prevents concurrent migration runs.

Uploaded images are stored on the local filesystem under `KNOWLEDGE_CORE_UPLOAD_DIR` (default `.uploads`), not in PostgreSQL. Back up the upload directory together with the database so document cover and inline image URLs remain valid.
