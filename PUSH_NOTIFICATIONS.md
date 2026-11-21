# Notificaciones de root

El servidor mantiene una bandeja de notificaciones por usuario y distribuye push por FCM en Android y APNs en iOS. Las claves privadas quedan exclusivamente en el servidor. No es necesario agregar Firebase a la base de datos ni usar Functions/Firestore.

```dotenv
PUSH_ENABLED=true
FIREBASE_PROJECT_ID=tu-proyecto
GOOGLE_APPLICATION_CREDENTIALS=./secrets/firebase-service-account.json

APNS_ENABLED=true
APNS_KEY_PATH=./secrets/AuthKey_XXXXXXXXXX.p8
APNS_KEY_ID=XXXXXXXXXX
APNS_TEAM_ID=XXXXXXXXXX
APNS_BUNDLE_ID=com.emnetwork.root
APNS_PRODUCTION=false
```

Cada proveedor está desactivado por defecto. Si se activa pero su configuración es inválida, el arranque falla explícitamente. El directorio `secrets/` está ignorado por Git. La cuenta de Google debe pertenecer al proyecto del `google-services.json` Android o tener permisos FCM sobre ese proyecto. `APNS_PRODUCTION=false` usa el sandbox de Apple para builds de desarrollo; debe cambiarse a `true` para TestFlight y App Store. No enviar ninguna credencial al frontend.

## Contratos autenticados

- `GET /v1/push/status?platform=android|ios` → `{ "enabled": true | false }` para el proveedor solicitado.
- `PUT /v1/push/devices/{installationUUID}` con `{ "token": "FCM_TOKEN", "platform": "android" }` → 204. Rechaza otros campos (incluyendo `userId`), plataformas, tokens inválidos y UUID inválido con 400; 503 si no está configurado; 500 si falla PostgreSQL.
- `DELETE /v1/push/devices/{installationUUID}` → 204 idempotente. Solo elimina instalaciones del usuario del JWT; invalida todos los trabajos pendientes de esa instalación mediante cascade.
- Todos requieren el JWT existente; sin autenticación → 401. No se expone un endpoint para enviar notificaciones arbitrarias ni para consultar tokens de otros usuarios.
- `GET /v1/notifications?limit=30&offset=0` devuelve la bandeja y el total no leído.
- `PATCH /v1/notifications/{notificationID}/read` y `POST /v1/notifications/read-all` actualizan lecturas únicamente para el usuario autenticado.
- `GET /v1/users/me/notification-preferences` y `PUT /v1/users/me/notification-preferences` consultan y actualizan las categorías `messages`, `events`, `social`, `crews`, `communities`, `activity` y `account`.

## Eventos y categorías

La bandeja recibe mensajes, nuevos seguidores, likes y comentarios, anuncios de comunidades, nuevos crews, estados de identidad y transferencias. Un scheduler idempotente agrega recordatorios para eventos marcados como “Voy”: el mismo día, cerca del comienzo y luego de una asistencia registrada para solicitar una reseña. Quitar “Voy” elimina los recordatorios futuros de ese evento.

Las preferencias se aplican al push y no borran el historial visible en la bandeja. Las rutas profundas se construyen en el cliente a partir de identificadores validados; el backend no acepta URLs arbitrarias de navegación.

## Persistencia y procesamiento

`PushRepository.InitSchema` crea `push_devices`, `push_jobs`, índices y trigger de forma transaccional/idempotente. Reutiliza `messages`, `chat_participants`, `users` y `message_receipts`.

El trigger encola solo mensajes text/image nuevos, por dispositivo de los participantes, excepto el autor. Entra en la misma transacción del mensaje; rollback no deja un aviso pendiente y reintentar el mismo UUID no crea otro trabajo. Un worker del API consulta esta **cola de entrega** cada dos segundos; no agrega polling al chat ni cambia el WebSocket.

`FOR UPDATE SKIP LOCKED` reclama un máximo de 10 trabajos, con leases de dos minutos y hasta cinco intentos. La misma consulta obtiene el nombre del emisor (o su alias si falta el nombre), el tipo del mensaje y una vista previa acotada, sin consultas por mensaje. Antes de enviar verifica cuenta/token actuales, membresía del chat y ausencia de lectura. El envío FCM muestra el emisor y hasta 240 caracteres Unicode del texto; para imágenes muestra “📷 Envió una foto” sin incluir enlaces privados. El nombre se limita a 80 caracteres. Estas vistas previas pueden aparecer en la pantalla bloqueada según los ajustes de Android. Usa el ícono `ic_stat_root`, color `#D4FF00`, canal `root_messages`, prioridad HIGH y TTL de 24 horas. El payload incluye `type=chat.message`, `chat_id`, `message_id`, `recipient_id`. El frontend valida destinatario y UUID antes de navegar. Nunca registrar tokens ni vistas previas en logs.

FCM UNREGISTERED elimina únicamente el token/cuenta/instalación todavía coincidentes. Un error de credenciales/proyecto/payload no borra dispositivos. Los reintentos usan backoff; no se procesan mensajes con más de 24 horas y se borran trabajos con más de siete días. Aceptación por FCM **no** marca como entregado/leído. No hay garantía exactly-once frente a timeouts/crashes; Android agrupa/reemplaza por tag de chat.

## Pruebas

```powershell
go test ./...
```

Para probar SQL en un esquema aislado que se elimina al finalizar:

```powershell
$env:PUSH_TEST_USE_LOCAL_ENV = "1"
go test ./internal/adapters/repository/postgres -run TestPushPostgresIntegration -v
```

Alternativamente definir `PUSH_TEST_DATABASE_URL` con una base PostgreSQL de prueba y permisos para crear un esquema. El test comprueba explícitamente el esquema activo antes de crear fixtures; no modifica usuarios ni conversaciones reales.

El formato FCM es de datos, sin el bloque `notification` automático: incluye `title` y `body` además de los IDs. Android lo renderiza con `RootMessagingService`, usando el glifo de Root como ícono pequeño y la identidad adaptativa de la aplicación para el ícono del sistema, sin una imagen grande adicional a la derecha. Android/MIUI controla la ubicación y el color del ícono. Esto permite el mismo aviso en primer y segundo plano sin descargar imágenes ni duplicar avisos del SDK. Actualizar la app Android antes de desplegar este formato en el backend; las apps anteriores no renderizan estos mensajes de datos.

La guía completa de instalación por USB está en `ANDROID_PUSH.md` del frontend. Web Push todavía no forma parte de esta integración.
