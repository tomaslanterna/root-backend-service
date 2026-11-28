# Afters + "Mandanos tu evento"

Backend de dos funciones nuevas:

- **Afters**: fiestas posteriores que suben usuarios con la identidad verificada. Pueden estar atadas a un evento o no.
- **Mandanos tu evento**: un usuario verificado manda un evento para revisión y un ADMIN lo publica o lo rechaza.

Código:

| Capa | Archivos |
|---|---|
| Dominio y reglas | `internal/core/domain/after.go`, `internal/core/domain/event_submission.go` |
| Puertos | `internal/core/ports/afters.go` |
| Servicios | `internal/core/services/after_service.go`, `internal/core/services/event_submission_service.go` |
| PostgreSQL | `internal/adapters/repository/postgres/after_repository.go`, `event_submission_repository.go`, `viewer_access_repository.go`; embed en `event_repository.go` (`GetEventByID`) |
| HTTP | `internal/adapters/handlers/after_handler.go`, `event_submission_handler.go`, `api_error.go` (errores, `RequireKyc`, `RequireAdmin`, subida de imágenes); rutas en `router.go` |
| Cableado | `cmd/api/main.go` |

## Reglas

### Identidad verificada (KYC)

- Solo `users.is_kyc_verified = true` puede crear o editar un after, subir su portada, mandar un evento o subir su flyer.
- La regla la imponen dos capas: el middleware `RequireKyc` en cada ruta marcada **[KYC]**, y el servicio, que la vuelve a revisar en `Create` y `Update`. Sin repositorio de acceso configurado, el middleware rechaza todo con 503.
- Una cuenta eliminada o suspendida (`deleted_at` / `suspended_at`) cuenta como no verificada.
- Respuesta: `403 {"status":403,"message":"Necesitás verificar tu identidad","code":"kyc_required"}`.

### Edad

- Ver el detalle de un after, pedir entrar o marcar "Voy" no pide KYC. Sí pide sesión y tener al menos 18 años según `users.dob`.
- **Decisión:** si `dob` está vacío o no tiene el formato `YYYY-MM-DD`, la edad se trata como no verificable y la respuesta es `403 underage` (se falla cerrado). El mensaje pide completar la fecha de nacimiento.
  - Al 27/11/2026, 11 de 37 usuarios de producción tienen `dob` vacío.
- La edad se calcula con el calendario de Montevideo.
- El host siempre ve su propio after.
- Los listados (`GET /v1/afters`, `GET /v1/events/:id/afters` y el embed) son públicos, porque nunca llevan la dirección.

### After

- Se publica al instante.
- Se **archiva** a las 24 h de empezar: `archivesAt = startsAt + 24h`. Desde ese momento solo lo ve el host. No hay cron: cada consulta filtra por fecha.
- Se considera **próximo o en curso** mientras `now < COALESCE(endsAt, startsAt + 12h)`. Los listados muestran solo estos.
- Un after **activo** es uno con `status = 'active'` que está próximo o en curso. Cada host puede tener como mucho **3 activos** (`429 too_many_active`). El tope se protege con un advisory lock por host, así que dos creaciones simultáneas no lo pasan.
- **Oficial** (`isOfficial`): `event_id` no es null y el host es `events.producer_id` o un RRPP del evento (`event_rrpps` y `users.role = 'RRPP'`). Lo calcula el backend; nunca viene del cliente. Alguien a quien le quitaron el rol RRPP deja de ser oficial.
- En el evento (embed y `/events/:id/afters`):
  - Aparecen todos (oficiales y de usuarios) desde que se publican, aunque el evento no haya empezado.
  - Orden: primero los oficiales, después por `startsAt`.
- Denuncias:
  - Con 3 denuncias de usuarios distintos, el after pasa a `status = 'hidden'`.
  - El restore de un ADMIN guarda `reviewed_at`, y desde ahí solo cuentan las denuncias nuevas.
  - El host no puede denunciar su propio after. Repetir una denuncia no suma.
- No tiene chat propio: el host deja un **aviso** (`notice`, ≤ 280).
- Cada decisión por defecto es una constante en `internal/core/domain/after.go`:

  | Constante | Valor |
  |---|---|
  | `AftersMaxActivePerHost` | 3 |
  | `AfterReportsToHide` | 3 |
  | `UserAftersWaitForEventStart` | true |
  | `AfterArchiveAfter` | 24 h |
  | `AfterDefaultDuration` | 12 h |
  | `AfterMaxLead` | 7 días |
  | `AfterStartGrace` | 15 min |
  | `AfterEventGrace` | 12 h |
  | `AfterMinAge` | 18 |

### Acceso, dirección y aviso

| Acceso | Cómo se entra | Quién ve la dirección |
|---|---|---|
| `free` | `join` → `going` | Todo el que ve el detalle |
| `approval` | `join` → `requested`; el host acepta o rechaza | El host y los `accepted` |
| `ticket` | `join` → `going` (precio + link de compra) | Todo el que ve el detalle |

- `address` solo sale en `GET /v1/afters/:id` y en las respuestas que devuelven un `AfterDetail`, y solo si el viewer puede verla. Si no puede, sale `null` (nunca un texto como "oculta").
- La dirección nunca sale en listados, embeds, push ni logs, y nunca se manda al moderador de contenido.
- El aviso (`notice`, `noticeUpdatedAt`) solo lo ven el host, los `accepted` y los `going`.
- El push del aviso no lleva el texto, porque podría tener la dirección.

### Cupo

- `capacity` (2-500, opcional) limita a `accepted + going`.
- `join` en free o ticket, y `accept` en approval, bloquean la fila del after con `SELECT … FOR UPDATE` y cuentan en otra sentencia después del lock.
  - En READ COMMITTED, una subconsulta dentro de la misma sentencia vería la foto previa al lock y dejaría pasar de más. Esto lo detectó el test de integración.
- Si el after está lleno: `409 after_full`.
  - Pedir entrar a un after `approval` que ya está lleno también da `after_full`.
- Bajar `capacity` por debajo de los que ya están da 400.

### Bloqueos

- Si el host bloqueó al viewer, o al revés (`user_blocks`), el viewer:
  - no ve los afters de ese host en ningún listado;
  - recibe 404 en el detalle;
  - no puede hacer `join`.
- Los hosts con la cuenta eliminada o suspendida desaparecen de todo.

## Contrato (base `/v1`)

Errores: `{"status": <http>, "message": "...", "code": "..."}`. Ojo: `status` es el número HTTP. En crews temporales es el string `"error"`.

### Tipos

```
AfterSummary = { id, name, coverUrl: string|null, startsAt, endsAt: string|null, archivesAt,
  zone, access: "free"|"approval"|"ticket", price: string|null, ticketUrl: string|null,
  capacity: number|null, acceptedCount, goingCount, isOfficial,
  host: {userId, name, username, avatarUrl, isKycVerified},
  event: {id, title, date, location, image} | null,
  genres: string[], viewerStatus: null|"host"|"requested"|"accepted"|"rejected"|"going" }

AfterDetail = AfterSummary + { rules, lineup, addressVisible, address, notice, noticeUpdatedAt,
  followedGoing: {count, preview: [{userId,name,username,avatarUrl}] (máx 4)}, canEdit,
  status: "active"|"hidden"|"removed"|"archived" }

EventSubmission = { id, name, startsAt, venueName, venueAddress, flyerUrl, ticketUrl, lineup: string|null,
  genres, submitterRole: "organizer"|"rrpp"|"fan", status: "pending"|"published"|"rejected",
  rejectReason, eventId, createdAt }
```

- `AfterDetail.status` es un **agregado** al contrato. Solo le sirve al host, que es el único que puede ver su after oculto o archivado.
- `followedGoing` cuenta a las personas que el viewer sigue (`users.following`) y que están `accepted` o `going`.
- `coverUrl`, `host.avatarUrl`, `event.image` y `flyerUrl` salen como URLs firmadas: se guardan como clave S3 y se firman al responder.

### Afters

| Método y ruta | Auth | Respuesta |
|---|---|---|
| `GET /events/:id` | opcional | El evento suma `afters: {total, preview: AfterSummary[] (máx 3), viewerAfterIds}` en la **misma consulta SQL** del detalle (subconsulta), sin otro request y sin N+1 |
| `GET /events/:id/afters?limit&offset` | opcional | `{data: AfterSummary[], meta:{total,limit,offset,hasMore}}`; 404 `event_not_found` |
| `GET /afters?from&until&zone&limit&offset` | opcional | Próximos o en curso que se solapan con `[from, until]` (`from` = ahora por defecto), `zone` por substring sin mayúsculas, orden `startsAt` ASC, `limit` 20 por defecto y 50 como máximo |
| `GET /afters/:id` | sesión, +18 | `AfterDetail`. Archivado u oculto: 404 `not_found` (el host sí lo ve, con su `status`). Eliminado: 404 para todos |
| `POST /afters/cover` (multipart `image`) | **[KYC]** | `{key, url}`; ≤ 8 MB; JPG, PNG o WebP detectado por el contenido; clave `afters/<userId>/<ts>.<ext>` |
| `POST /afters` | **[KYC]** | `201 AfterDetail` |
| `PATCH /afters/:id` | **[KYC]**, host | `200 AfterDetail`; 403 `not_host`; archivado: 409 `after_ended` |
| `DELETE /afters/:id` | host | `204`; soft delete (`status = 'removed'`); idempotente |
| `POST /afters/:id/join` `{message?}` (≤ 280) | sesión, +18 | `200 AfterDetail` actualizado |
| `DELETE /afters/:id/join` | sesión | `204`; cancela el pedido o deja de ir; idempotente |
| `GET /afters/:id/requests?status=requested` | host | `{data:[{userId,name,username,avatarUrl,message,createdAt,status}]}`; `status` puede ser `requested` (por defecto), `accepted`, `rejected` o `going` |
| `POST /afters/:id/requests/:userId/accept` y `/reject` | host | `204` |
| `POST /afters/:id/report` `{reason}` (1-280) | sesión | `204`; una denuncia por usuario |

`POST /afters`:

- Body: `{name(1-60), eventId?, startsAt, endsAt?, zone(1-60), address(1-200), access, price?(≤40, acepta texto o número), ticketUrl?, capacity?(2-500), genres?(≤5, cada uno ≤30), lineup?(≤120), rules?(≤500), coverKey?}`.
- Validaciones:
  - `startsAt` va entre ahora (con 15 min de tolerancia) y 7 días adelante.
  - Si hay `eventId`, `startsAt` tiene que ser igual o posterior al inicio del evento.
  - `endsAt` tiene que ser posterior a `startsAt` y estar como mucho 24 h después.
  - `ticketUrl` es obligatorio si `access = "ticket"` y tiene que ser https.
  - Con `free` o `approval` se descartan `price` y `ticketUrl`.
  - `coverKey` tiene que estar bajo `afters/<tu userId>/`: nadie puede usar archivos ajenos.
- Errores:
  - 3 activos: `429 too_many_active`.
  - `eventId` inexistente o terminado hace más de 12 h: `422 invalid_event`.

`PATCH /afters/:id`:

- Acepta los mismos campos que `POST /afters`, más `notice?` (≤ 280).
- `null` borra los campos opcionales; los obligatorios no se pueden borrar.
- La ventana de `startsAt` y el evento se revalidan solo si cambiaron.
- Un `notice` nuevo dispara el push `after.notice`.

`POST /afters/:id/join`:

- `approval` → `requested` y push al host. `free` o `ticket` → `going`.
- Errores:
  - `409 after_full`.
  - `409 already_joined`: ya pidió, ya va, fue rechazado o es el host. **Decisión:** quien fue rechazado no puede volver a pedir.
  - `409 after_ended`.
  - `403 underage`.
  - `404 not_found`: oculto, archivado, eliminado o con bloqueo.

`POST /afters/:id/requests/:userId/accept` y `/reject`:

- Errores:
  - 404 `request_not_found`.
  - 409 `request_not_pending`.
  - 409 `after_full`.
  - 409 `after_ended`: solo al aceptar.

`POST /afters/:id/report`:

- Repetir la denuncia no suma y responde 204 igual.
- El host no puede denunciar su propio after: 400.

### Mandanos tu evento

| Método y ruta | Auth | Respuesta |
|---|---|---|
| `GET /events/similar?name=&date=` | opcional | `{data: Event[] (máx 3)}` |
| `POST /event-submissions/flyer` (multipart `image`) | **[KYC]** | `{key, url}`; clave `event-submissions/<userId>/<ts>.<ext>` |
| `POST /event-submissions` | **[KYC]** | `201 EventSubmission`; 5 pendientes como máximo: `429 too_many_pending` |
| `GET /users/me/event-submissions` | sesión | `{data: EventSubmission[]}`, los más nuevos primero (hasta 100) |

`GET /events/similar`:

- Busca eventos cuyo título contiene cada palabra del nombre (palabras de 3 letras o más, hasta 4; sin mayúsculas).
- Con `date`, busca en ±36 h y ordena por cercanía a esa fecha.
- Sin `date`, busca eventos que no terminaron hace más de 1 día.
- Con menos de 3 letras devuelve `[]`.

`POST /event-submissions`:

- Body: `{name(1-120), startsAt, venueName(1-120), venueAddress(1-200), flyerKey, ticketUrl?(https), lineup?(≤300, texto o lista de textos), genres[](≤5), submitterRole, contact(1-120), rightsConfirmed: true}`.
- `startsAt` puede ir desde una hora atrás hasta un año adelante.
- `flyerKey` tiene que estar bajo `event-submissions/<tu userId>/`.

ADMIN (`users.role = 'ADMIN'`, con el middleware `RequireAdmin` más el servicio; si no, `403 forbidden`):

| Método y ruta | Respuesta |
|---|---|
| `GET /admin/event-submissions?status=pending` | `{data: [EventSubmission + {contact, submitter:{userId,name,username,avatarUrl}}]}`, los más viejos primero |
| `POST /admin/event-submissions/:id/publish` | Crea el evento y devuelve `{eventId}`. Si ya fue revisado: `409 not_pending` |
| `POST /admin/event-submissions/:id/reject` `{reason}` (1-280) | `204` |
| `GET /admin/afters?status=hidden` | `{data: [AfterSummary + {status, reportsCount, reasons (últimos 5)}]}`, sin la dirección; `status` puede ser `hidden` (por defecto), `active` o `removed` |
| `POST /admin/afters/:id/restore` y `/remove` | `204` |

Cómo publicar crea el evento (**decisiones**):

| Campo de `events` | Valor |
|---|---|
| `title` | `name` |
| `date` | `startsAt` pasado a hora de Montevideo sin zona, igual que el resto de los eventos |
| `location` | `venueName` |
| `description` | `"Dirección: <venueAddress>"` |
| `cinematic_banner_url` | la clave del flyer |
| `lineup` | el texto separado por `,` `/` `;` o saltos de línea |
| `genre` | los géneros unidos por coma (≤ 100) |
| `ticket_url` | `ticketUrl` |
| `country` | el del remitente, o `UY` |
| `producer_id` | vacío: no se le da al remitente el rango de organizador (ni los afters oficiales) sin verificarlo. Un ADMIN lo puede cargar después |

No se crean filas en `event_artists`.

### Códigos de error

| HTTP | `code` |
|---|---|
| 400 | `invalid_request`, `invalid_id` |
| 401 | `unauthorized` (o el texto plano de `AuthMiddleware`) |
| 403 | `kyc_required`, `underage`, `not_host`, `forbidden` |
| 404 | `not_found`, `event_not_found`, `request_not_found` |
| 409 | `after_full`, `already_joined`, `after_ended`, `request_not_pending`, `host_cannot_leave`, `not_pending` |
| 413 | `too_large` |
| 415 | `unsupported_media_type` |
| 422 | `invalid_event`, `content_policy` |
| 429 | `too_many_active`, `too_many_pending` |
| 500 | `internal_error` |
| 503 | `unavailable` |

Moderación (Gemini, `ContentModerationService`):

- Revisa `name`, `zone`, `rules`, `lineup` y `notice` del after (en un PATCH, solo lo que cambió), el `message` del join, y el nombre, lugar y lineup del envío.
- La dirección y el contacto no se mandan nunca.
- Si el texto infringe las normas: `422 content_policy`.

## Push

Usan el mismo mecanismo que `crew.join_request`: un INSERT en `notifications` y el trigger encola el envío a FCM y APNs. El adapter suma `type`, `category`, `title` y `body` a `data`.

| Type | A quién | Categoría | `data` |
|---|---|---|---|
| `after.join_request` | host | `crews` | `id`, `after_id`, `user_id`, `url: /afters/:id` |
| `after.join_request.accepted` | quien pidió | `crews` | `id`, `after_id`, `url` |
| `after.join_request.rejected` | quien pidió | `crews` | `id`, `after_id`, `url` |
| `after.notice` | aceptados y quienes van | `events` | `id`, `after_id`, `url` |
| `event_submission.published` | quien mandó el evento | `events` | `id`, `submission_id`, `event_id`, `url: /events/submissions` |
| `event_submission.rejected` | quien mandó el evento | `events` | `id`, `submission_id`, `url: /events/submissions` |

- Ningún push lleva la dirección ni el texto del aviso.
- Dedupe:
  - `after.join_request`: uno por persona y after (cancelar y volver a pedir no vuelve a notificar).
  - `after.notice`: uno por versión del aviso.

## Tablas

Las crean `AfterRepository.InitSchema` y `EventSubmissionRepository.InitSchema` al arrancar el API.

- Usan `CREATE TABLE IF NOT EXISTS` y `CREATE INDEX IF NOT EXISTS`, dentro de una transacción con `pg_advisory_xact_lock`.
- Son idempotentes y no tocan tablas existentes.

| Tabla | Columnas | Índices |
|---|---|---|
| `afters` | `id` uuid pk, `host_user_id` → users (cascade), `event_id` → events (set null), `name`, `zone`, `address`, `access` (check), `price_text`, `ticket_url`, `capacity` (2-500), `genres` text[], `lineup`, `rules`, `cover_key`, `notice`, `notice_updated_at`, `starts_at`, `ends_at` (> `starts_at`), `status` (active/hidden/removed), `reviewed_at`, `reviewed_by` → users, `created_at`, `updated_at` | `(event_id, starts_at) WHERE status='active'`, `(starts_at) WHERE status='active'`, `(host_user_id)`, `(event_id) WHERE NOT NULL` (FK), `(reviewed_by) WHERE NOT NULL` (FK), `(status, updated_at DESC) WHERE status<>'active'` (moderación) |
| `after_attendees` | pk `(after_id, user_id)`, `status` (requested/accepted/rejected/going/cancelled), `message`, `created_at`, `updated_at` | `(after_id, status)`, `(user_id)` (FK) |
| `after_reports` | pk `(after_id, user_id)`, `reason`, `created_at` | `(user_id)` (FK) |
| `event_submissions` | `id`, `submitter_user_id` → users, `name`, `starts_at`, `venue_name`, `venue_address`, `flyer_key`, `ticket_url`, `lineup`, `genres`, `submitter_role` (check), `contact`, `rights_confirmed` (check true), `status` (check), `reject_reason`, `published_event_id` → events, `reviewed_by` → users, `reviewed_at`, `created_at` | `(submitter_user_id, created_at DESC)`, `(status, created_at)`, `(published_event_id)` y `(reviewed_by)` parciales (FK) |

`afters.reviewed_at` y `reviewed_by` no estaban en la spec. Se agregaron para que el restore reinicie la cuenta de denuncias.

El embed `afters` del detalle del evento depende de estas tablas y de `event_rrpps`:

- `main.go` lo activa (`postgres.EnableAftersEmbed`) solo si `AfterRepository.SchemaReady` confirma que existen.
- Si no existen, el detalle del evento sigue igual que antes, sin `afters`.

## Fechas

- `events.date` no tiene zona y guarda la hora de Montevideo, como el resto de `event_repository.go` y como lo lee el front (`parseEventRealDate` le pone `-03:00`). Para comparar con instantes reales (afters, `NOW()`) se convierte con `e.date AT TIME ZONE 'America/Montevideo'`. En `event.date` de los afters va con el mismo formato que el resto de los eventos: la hora de Montevideo con `Z`.
- El inicio efectivo de un evento es `date`; el fin, `COALESCE(end_date, date + 12h)`.

## Tests

| Test | Qué cubre |
|---|---|
| `internal/core/domain/after_test.go` | Edad (incluido `dob` vacío o con otro formato), visibilidad de la dirección y del aviso por acceso y estado, validaciones, PATCH con null, envíos |
| `internal/core/services/after_service_test.go` | KYC al crear y editar, moderación sin la dirección, `invalid_event`, edad y visibilidad del detalle (oculto, archivado, eliminado, host), "Voy" de menores, solo el host edita, ADMIN, envíos |
| `internal/adapters/handlers/afters_access_test.go` | Router real con JWT: **cada endpoint [KYC]** responde 403 `kyc_required` a un usuario sin verificar sin llegar al servicio; endpoints ADMIN; middleware fail-closed; tabla de códigos de error; `AfterSummary` sin `address` |
| `internal/adapters/repository/postgres/after_repository_integration_test.go` | **Opt-in**, en un esquema aislado y aleatorio que se borra al terminar: embed en la misma consulta del detalle (oficial vs no oficial, antes y después de que empiece el evento, ex RRPP, `viewerAfterIds`), 3 activos con creaciones simultáneas, cupo con "Voy" y aceptaciones simultáneas, dirección (pedido vs aceptado; nunca en listados, embeds ni push), aviso, bloqueos, archivado y terminado, denuncias → hidden → restore, uso de índices, envíos (tope, publicar, rechazar, similares) |

Para correr el de integración:

```
AFTERS_TEST_USE_LOCAL_ENV=1 go test ./internal/adapters/repository/postgres -run TestAftersPostgresIntegration -v
# o AFTERS_TEST_DATABASE_URL=<base de pruebas>
```

## Migrar producción

1. Hacer el deploy del API con `-initialize-schema=true`, que es el valor por defecto. Al arrancar crea `afters`, `after_attendees`, `after_reports` y `event_submissions` con sus índices.
   - Son tablas nuevas: no bloquean tablas existentes y no reescriben datos.
2. Si el API corre con `-initialize-schema=false`, hay que correr una vez una instancia con `true`, o ejecutar a mano los mismos `CREATE` de los dos `InitSchema`.
   - Hasta que existan las tablas, el detalle del evento sigue sin el embed `afters` (`SchemaReady`).
   - Los endpoints de afters, en cambio, van a devolver 500.
3. Para usar las rutas `/admin/*` hace falta al menos un usuario con `role = 'ADMIN'`. Hoy en producción solo existen los roles `USER` y `RRPP`.
4. Revisar que S3 esté configurado, porque las portadas y los flyers lo necesitan.
