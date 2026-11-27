# Migraciones de base de datos

El esquema de PostgreSQL se aplica con [goose](https://github.com/pressly/goose) al arrancar la API
(`cmd/api`, flag `-initialize-schema`, activo por defecto). Las versiones aplicadas quedan en la tabla
`goose_db_version`. Un advisory lock de Postgres evita que dos instancias migren a la vez: la segunda
espera y después no encuentra nada pendiente.

## Versiones

| Versión | Archivo | Qué hace |
|---|---|---|
| 1 | `baseline.go` | El esquema que antes cada repositorio aplicaba en cada arranque con `InitSchema`, en el mismo orden. Todo es idempotente: en una base que ya lo tenía no cambia nada, solo registra la versión. |
| 2 | `sql/00002_event_rrpps.sql` | Tabla `event_rrpps` (su `InitSchema` nunca se llamaba al arrancar). |

Las tablas base (`users`, `events`, `squads`, `chats`, `messages`, `posts`, `communities`, …) se crearon
a mano antes de que el esquema viviera en el código, así que el baseline asume que existen. Para poder
crear una base desde cero hace falta agregar su definición (a partir de un `pg_dump --schema-only`).

## Agregar un cambio de esquema

1. Crear `sql/NNNNN_descripcion.sql` con el número siguiente (cinco dígitos):

   ```sql
   -- +goose Up
   ALTER TABLE users ADD COLUMN IF NOT EXISTS bio TEXT;

   -- +goose Down
   ALTER TABLE users DROP COLUMN IF EXISTS bio;
   ```

2. Cada archivo corre una sola vez y dentro de una transacción. Para algo que no puede ir en una
   transacción (por ejemplo `CREATE INDEX CONCURRENTLY`), poner `-- +goose NO TRANSACTION` al principio.
3. No editar una migración que ya llegó a producción ni los `InitSchema` del baseline: hacer una nueva.
4. Recordar las reglas de `AGENTS.md` (por ejemplo, nada de `''` en columnas `JSONB`).

## Probar

```sh
MIGRATIONS_TEST_DATABASE_URL=postgres://... go test ./internal/adapters/repository/postgres/migrations -v
```

Cada test crea un esquema aleatorio con las tablas base, migra y lo borra al terminar. Comprueba que una
base existente no cambia (salvo `event_rrpps`), que una nueva queda igual a una existente y que varias
instancias en paralelo no chocan.
