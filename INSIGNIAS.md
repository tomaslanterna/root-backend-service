# Insignias de asistencia y reseñas

## Integración

Se reutiliza `EventRepository.GetLiveEventStatus`, que ya alimentaba el aviso
«Estás aquí»: JWT, RSVP `going`, coordenadas de la sede, radio configurado
(300 m si es NULL) y fecha entre hace 12 horas y dentro de 2 horas. La hora
proviene de PostgreSQL. Ese resultado ahora inserta una asistencia permanente
en la misma transacción, antes de responder al cliente. Los reintentos y
consultas concurrentes no duplican ni cambian la fecha original.

Una asistencia es también la insignia: no hay una tabla paralela de premios ni
un endpoint para fabricar insignias. La sincronización de pasos utiliza esta
misma validación; no acepta como presencia un evento sin coordenadas.

La notificación de presencia existente es el banner **en la aplicación**, no
un push FCM de eventos. Su enlace abre `/events/{id}/survey`, que permite
crear la valoración o consultar la ya confirmada. Las notificaciones push de chats no se modifican.
La entrega no depende de abrir el banner ni de guardar una reseña.

El detector conserva su activación al iniciar el proveedor de sesión. Al llegar
después de haber abierto la aplicación, reabrir/recargar permite comprobar la
presencia. No se agregó otro detector ni un segundo proveedor. Se mantienen
los límites de confianza del GPS del cliente: no es una solución antifraude
contra GPS falsificado.

## Persistencia y migración

`BadgeRepository.InitSchema` crea `event_attendance`:

- `user_id UUID`, FK a usuarios con borrado en cascada.
- `event_id UUID`, FK a eventos con borrado restringido para conservar recuerdos.
- `confirmed_at TIMESTAMPTZ`, primera confirmación del servidor.
- `source TEXT`, restringido a `geofence` o `manual`.
- `grant_reason TEXT`, obligatorio (1–500 caracteres) para una asignación manual.
- `departed_at TIMESTAMPTZ` nullable, salida confirmada explícitamente por el dueño.
- `reminder_offered_at TIMESTAMPTZ` nullable, invitación ya ofrecida persistentemente.
- PK `(user_id,event_id)` e índice de colección por usuario y fecha descendente.

Se agregan `events.department` y `events.country` nullable, sin inferir ni rellenar
datos históricos. `event_review_openings` persiste `(user_id,opening_id)` único,
hora del servidor y si ya ofreció una invitación. Se indexan aperturas por fecha
y asistencias pendientes de recordatorio, sin crear otra tabla de insignias.

Se reutiliza `event_surveys`, con su unicidad usuario/evento, agregando
`updated_at` y checks de 0 a 5. El guardado es una confirmación atómica única.
`ON CONFLICT DO NOTHING RETURNING` impide sobrescribir reseñas existentes y
devuelve `ErrSurveyConfirmed` (HTTP 409), incluso con solicitudes concurrentes.
Todas las notas de DJs se insertan junto al evento y el texto en una transacción.
Se reutilizan los criterios opcionales y `survey_artist_ratings`; las reseñas
históricas siguen disponibles, pero ya no pueden editarse. Verifica
que cada artista pertenezca al lineup de ese evento; una entrada ajena revierte
toda la transacción. La asistencia se bloquea durante el guardado para coordinar
la comprobación de pendientes del recordatorio. La reseña admite hasta 1000 caracteres Unicode
y se recorta. `0` es una valoración válida; ausencia de registro significa
sin valorar.

La FK compuesta desde las reseñas a `event_attendance` se agrega `NOT VALID`:
preserva reseñas históricas, pero controla nuevas escrituras. Además se
comprueba la asistencia antes del insert. No se fabrican asistencias para
hacer pasar datos anteriores. Las antiguas sesiones de baile no almacenan
evidencia de ubicación y su validación permitía sedes sin coordenadas: no son
una base fiable para un backfill. Reservas, entradas y RSVPs tampoco bastan.

Las inicializaciones son transaccionales, idempotentes y fallan de forma
explícita. Los entrypoints de API y migración inicializan asistencia antes de
reseñas. **Desplegar/migrar backend antes del frontend.** No ejecutar el API
local con una DATABASE_URL de producción para una prueba de interfaz.

## Contratos

### Asignaciones manuales autorizadas

`cmd/grant-badges` permite asignar insignias con acceso administrativo a la DB,
sin endpoint público. Requiere email, UUID previamente verificado, IDs de eventos
y motivo. Por defecto solo hace una consulta previa; `-apply` aplica la migración
de insignias/reseñas y los premios. Verifica la cuenta única y los eventos;
el insert es transaccional, idempotente y conserva registros previos. Guarda
`source=manual`, motivo y fecha real de asignación del servidor, no una fecha de
asistencia inventada. No crea pasos, valoraciones, RSVPs ni artistas.

La respuesta incluye `source`, y el detalle identifica los premios manuales.
Estos no acreditan presencia GPS ni evitan las comprobaciones geofence de pasos.
La preparación compatible de reseñas no agrega la FK de asistencia a la API live
antigua, para no bloquear sus escrituras durante la prueba local; el API nuevo
mantiene la validación de asistencia en cada guardado. El despliegue definitivo
con `InitSchema` continúa incorporando la FK.

Una instancia local completa contra la DB live requiere autorización explícita.
Luego de esta migración acotada, iniciarla con `-initialize-schema=false
-background-workers=false -listen-address=127.0.0.1:8080`: no modifica otros
esquemas ni procesa colas compartidas con producción. No equivale a desplegar
los endpoints nuevos en el backend publicado. La API normal conserva sus defaults.

- `GET /v1/users/{username}/badges?limit=12&offset=0`: público con JWT opcional.
  `{data:[EventBadge],meta:{total,limit,offset,hasMore}}`. Máximo 50.
  Orden `confirmed_at DESC,event_id DESC`. La reseña y puntuación personal
  solamente se incluyen si el JWT corresponde al dueño del perfil; para otros
  visitantes `review` es NULL. `Cache-Control: private, no-store`.
- `GET /v1/events/{id}/badge`: autenticado, devuelve la propia insignia y reseña.
  401 sin sesión, 400 UUID inválido, 404 sin asistencia, 500 fallo interno.
- `GET /v1/events/{id}/badge/flyer`: imagen oficial pública JPEG/PNG/WebP,
  máximo 8 MB, caché de una hora; resuelve la clave S3 del evento, nunca uploads
  de crews ni KYC. No permite URLs arbitrarias (solo el host legado autorizado
  images.unsplash.com, HTTPS sin redirects). 404 si no existe imagen.
- `GET /v1/events/{id}/badge/artists/{artistID}/image`: retrato registrado,
  solo si el artista está asociado al evento indicado, mismas restricciones.
- `POST /v1/events/{id}/attendance/checkout`: JWT, cuerpo vacío, 204. Requiere
  asistencia geofence propia y fecha del evento <= NOW(). Guarda departed_at
  idempotente; manuales, futuras y ajenas devuelven 404. No usa userId cliente.
- `POST /v1/users/me/review-reminder/open`: JWT, `{openingId:UUID}`, devuelve
  `{badge:EventBadge|null}`, sin caché privada. Registra la hora del servidor;
  como máximo una invitación por apertura y una por evento. Cuerpo/UUID inválido
  devuelve 400; sin JWT 401. No acepta usuario ni hora enviados por el cliente.
- `POST /v1/events/{id}/surveys`: ruta existente autenticada. Requiere
  `general_rating` entero 0–5; opcional `comment`. Mantiene los demás campos
  existentes. 200 tanto al crear como al editar, 400 datos inválidos, 401 sin
  sesión, 403 sin asistencia, 500 fallo interno. No acepta userId del cliente.
- `GET /v1/users/me/pending-surveys`: solamente asistencias confirmadas sin reseña,
  después del inicio del evento, máximo 50. Un RSVP solo ya no habilita reseñar.
- `GET /v1/events/live-status?lat=...&lng=...`: ruta existente autenticada.
  Confirma/persiste antes de devolver `attendanceConfirmed:true` con `isLive:true`.
  Coordenadas ausentes, no finitas o fuera de rango reciben 400.

Cada `EventBadge` contiene `eventId`, `title`, `imageUrl`, `eventDate`,
`location`, `department`, `country`, `genre`, `confirmedAt`, `departedAt`,
`headliner`, `headliners`, `review` nullable, `steps`
nullable, `mostDanced` y `lineupNames`. El detalle añade `lineup` reutilizando
`ArtistRepository.GetEventLineup`, ordenado por horario y artist_id, con las
tablas reales `event_artists`/`artists`. No se consultan artistas por cada
elemento de la colección.
`headliner` contiene `{id,name,imageUrl}` del artista marcado `is_headliner`
en las tablas existentes `event_artists`/`artists`; se desempata por horario
e ID. Si falta, es NULL: no se deducen nombres a partir del título en producción.
Las listas consultan total + página y resuelven el artista en la misma consulta,
sin requests ni consultas adicionales por insignia.

## Salida y apertura posterior

No existía un mecanismo fiable de salida/check-out. No se presenta perder GPS,
responder un mensaje o terminar la fecha del evento como evidencia de salida.
Se agregó una confirmación voluntaria «Terminé mi noche», sin cambiar check-in,
detector, contador ni permisos. El cliente registra su apertura ANTES del checkout.
Una pendiente solo puede ofrecerse si departed_at es anterior a la apertura
registrada en servidor y todavía no existe reseña. No se dispara en el checkout.
Las insignias manuales no acreditan presencia y quedan excluidas.

La web registra un nuevo documento (incluye recargar), no cada ruta/visibilidad.
Capacitor registra también regresar desde segundo plano. Se selecciona la salida
más reciente, luego event_id descendente. Transacciones y locks evitan ofertas
duplicadas concurrentes; guardar una reseña bloquea la asistencia compatible.
«Ahora no» no crea una reseña: conserva la insignia pendiente, pero la invitación
ya queda ofrecida para no insistir. No hay push ni polling adicionales.

La preparación acotada para un API live antiguo puede ejecutarse con
`go run ./cmd/migrate -badges-only -compatible`. No migra otros módulos ni agrega
la FK de asistencia incompatible con escritores antiguos. El despliegue normal
conserva la migración estricta. Se ejecutó esta preparación sobre los datos reales
autorizados, sin crear pasos, reseñas, artistas, ubicaciones ni premios adicionales.

## Pasos y reconocimiento anual

Se reutiliza `dance_sessions`, sin modificar el detector ni sus permisos.
Se suman `steps_count` validados y no negativos por usuario/evento. El CTE
calcula esos totales y un ranking anual sobre la colección completa del dueño,
antes de aplicar limit/offset o seleccionar una sola insignia. El año procede
de `confirmed_at AT TIME ZONE 'America/Montevideo'`, coherente con la agrupación
de asistencia del frontend. Mayor total **positivo** gana; en empates, primera
confirmación más reciente y luego event_id descendente. Sin sesiones, `steps`
es NULL y no hay premio; sesiones reales con 0 conservan el 0 sin otorgarlo.
La distinción no depende de tener una valoración.

InitSchema añade un índice parcial `(user_id,event_id)` para pasos validados
no negativos solo cuando ya existe `dance_sessions`. No crea una tabla de
pasos paralela. Si no existe, devuelve NULL y false en lugar de fabricar datos.

## Archivos principales

- Dominio/puertos/servicio: `internal/core/domain/event_badge.go`,
  `event_survey.go`, `internal/core/ports/badges.go`,
  `internal/core/services/badge_service.go`, `dance_service.go`.
- PostgreSQL: `badge_repository.go`, `event_repository.go`,
  `event_survey_repository.go` dentro de `internal/adapters/repository/postgres`.
- HTTP: `badge_handler.go`, `event_handler.go`, `survey_handler.go`, `router.go`
  dentro de `internal/adapters/handlers`; `internal/services/survey/survey_service.go`.
- Wiring: `cmd/api/main.go`, `cmd/migrate/main.go`.

## Pruebas

### DJs ficticios privados para una prueba autorizada

`cmd/example-lineup` prepara tres artistas (DJ 1, DJ 2 y DJ 3) únicamente
para una cuenta verificada y sus insignias de origen manual con lineup vacío.
Requiere email, UUID esperado del usuario y UUIDs explícitos de los eventos.
Es solo lectura por defecto; `-apply` ejecuta la preparación idempotente.
Los artistas llevan `is_example`, `example_user_id` y `example_event_id` en
la entidad existente `artists`; tienen un constraint de ámbito e índice parcial.
Nunca se insertan en `event_artists` ni se publican como lineup real.
El detalle privado de la insignia los devuelve con `isExample: true`.
Las puntuaciones utilizan `survey_artist_ratings`, en la misma confirmación atómica,
solo para el dueño de esa insignia/evento y mientras no haya lineup oficial.
No se inventan reseñas, notas, fotos ni asistencias. Los datos reales anteriores
permanecen intactos. La prueba SQL comprueba duplicados, edición, aislamiento de
usuarios y que el lineup oficial siga vacío. La confirmación posterior devuelve
409 sin sobrescribir ninguna puntuación.

`go test ./...` ejecuta tests HTTP, servicio y las pruebas existentes. La
integración SQL se salta si no se configura explícitamente una base:

```powershell
$env:BADGES_TEST_DATABASE_URL='postgres://...'
go test ./internal/adapters/repository/postgres -run TestBadgesPostgresIntegration -count=1 -v
```

Alternativamente `BADGES_TEST_USE_LOCAL_ENV=1` lee DATABASE_URL del `.env`.
Se crea un esquema aleatorio `badges_test_*`, se verifica su `search_path`
antes de escribir, y se elimina con defer al terminar. No escribe ni migra
tablas públicas. Comprueba idempotencia/concurrencia, geofence, ventana temporal,
permanencia, paginación, privacidad y edición sin duplicación. También comprueba
pasos ausentes y cero, sesiones ajenas/no validadas, ranking entre páginas,
empates, cambio de año local y ausencia de la tabla opcional de pasos.

Para QA visual, `BADGES_TEST_PREVIEW_PORT=8082` activa un servidor **solo
localhost**, dentro de ese test y por cuatro minutos. Usa handlers reales para
insignias/reseñas y fixtures auxiliares de perfil/login, nunca el API productivo.
Dejar finalizar normalmente el test para que se elimine su esquema. No matar
el proceso antes del defer. El frontend de QA debe usar otro puerto y un
build temporal; restaurar el build normal y cerrar ese servidor al terminar.

Para una sesión manual más larga, `BADGES_TEST_PREVIEW_DURATION=30m` permite
ampliar el plazo hasta una hora; usar `go test -timeout 40m` para esa sesión.
Los datos auxiliares de perfil son ficticios y solo existen en este test.

`BADGES_TEST_PREVIEW_EVENTS_URL` permite importar, mediante GET público paginado,
el catálogo real en el esquema aislado, reemplazando únicamente sus fixtures.
Los DELETE validan `current_schema()` y califican explícitamente los targets.
Siete eventos se asignan SOLO al usuario ficticio `one`, sin inventar artistas,
lineups, retratos ni pasos. Se conservan
imágenes, títulos y fechas reales del catálogo. Son premios DEMO, no presencias
reales; jamás se usa este helper en `cmd/api` ni se modifica el esquema público.
Para una hora de prueba usar duración `1h` y timeout Go superior, por ejemplo `70m`.

`BADGES_TEST_PREVIEW_COPY_FROM=http://localhost:8083` permite preservar fechas
de asistencia y reseñas personales al renovar una muestra. Solo acepta una
fuente localhost y un destino `badges_test_*`; no sirve para escribir datos
de producción. El esquema se elimina al concluir normalmente el test.

`BADGES_TEST_PREVIEW_SAMPLES=1`, junto al catálogo de siete insignias DEMO,
permite cargar explícitamente seis totales de pasos sintéticos y hasta cuatro
reseñas de muestra (5, 4, 3 y 0). Valida el esquema `badges_test_*` y el usuario
ficticio `one`; usa tablas calificadas y no reemplaza reseñas manuales copiadas.
La query real verifica los totales y el reconocimiento anual. Este helper
solo se compila en tests: no es un fallback ni un endpoint de producción.
