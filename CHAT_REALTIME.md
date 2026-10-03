# Chat en tiempo real

El frontend usa una conexión WebSocket nativa por sesión. El backend Go usa Gorilla WebSocket y la base PostgreSQL existente para distribuir eventos entre instancias. Los mensajes continúan enviándose por HTTP; las actualizaciones llegan por WebSocket, sin polling.

## Contratos

- `GET /v1/chats/ws`: upgrade WebSocket, sin query parameters. El navegador debe enviar en cinco segundos el primer frame `{ "type": "authenticate", "token": "<JWT actual>" }`. Nunca poner el JWT en una URL. El servidor verifica el JWT actual y cierra con código `4401` al expirar. El cliente cierra su sesión en ese caso.
- Eventos salientes: `{ "type": "ready" }`, `{ "type": "resync" }`, `{ "type": "chat.created", "chat_id": "UUID" }`, `{ "type": "message.created", "chat_id": "UUID", "message": { ... } }`, `{ "type": "message.updated", "chat_id": "UUID", "message": { ... } }`. Los mensajes llevan los nombres existentes `sender_id`, `timestamp`, `status`, `read_at` y `delivered_at`. La pertenencia a la conversación se verifica antes de entregar cada evento; el estado se calcula para su destinatario.
- `POST /v1/chats/{id}/messages`: `{ "content": "texto", "type": "text", "client_message_id": "UUID generado una sola vez" }`. Máximo 4000 caracteres; clientes pueden enviar `text` o `image`, nunca `system`. La inserción y actualización del resumen son una transacción. Reintentar la misma operación con el mismo UUID devuelve el mensaje existente. Cambiar su contenido o reutilizar el UUID en otro chat/usuario devuelve `409`.
- `GET /v1/chats/{id}/messages`: las últimas 50 entradas, en orden cronológico con desempate por ID. `?before=<timestamp RFC3339>|<UUID>` pagina hacia atrás sin duplicados. Leer el historial no cambia los recibos. `after_timestamp` se conserva para clientes antiguos y no se utiliza para polling.
- `POST /v1/chats/{id}/receipts`: `{ "message_ids": ["UUID"], "read": false }` confirma recepción; `read: true` confirma visualización. Máximo 100 IDs. JWT obligatorio; el servidor ignora IDs ajenos al chat y mensajes propios. Cada recibo es único por mensaje y receptor. El endpoint antiguo `PATCH .../messages/read` sigue disponible para compatibilidad, pero el frontend usa únicamente recibos explícitos.
- `GET /v1/chats`: array de conversaciones, participantes, `last_message`, `updated_at`, `unread_count`. La consulta carga los participantes por lote.

Estados: `sent` significa persistido; `delivered`, recibido por todos los otros participantes actuales; `read`, leído por todos ellos. Los mensajes entrantes muestran el recibo personal del usuario actual. `sending` y `failed` son estados locales. El frontend solo envía lectura cuando el mensaje está visible en una conversación abierta y la pestaña está visible y enfocada. Responder no infiere lectura.

## Reconexión y distribución

Los triggers de `messages` y `message_receipts` publican únicamente IDs en `root_chat_events`. PostgreSQL entrega las notificaciones después del commit. Cada instancia mantiene un listener y distribuye solo a conexiones de los participantes actuales. La base conserva el historial y los recibos; LISTEN/NOTIFY no es una cola durable.

El frontend se suscribe antes de cargar el historial, combina eventos/HTTP por UUID y evita degradar estados por respuestas tardías. Reconecta con espera progresiva y variación aleatoria, y sincroniza al recibir `ready`, `resync` o al volver a una pestaña. Si había más de 50 mensajes perdidos, pagina hasta alcanzar el historial ya cargado y actualiza sus recibos. No hay intervalo de consulta de mensajes. Un cliente lento se desconecta para recuperar su estado al reconectar.

## Configuración y despliegue

- Frontend: `NEXT_PUBLIC_BACKEND_API_URL` debe apuntar a este backend. `https` se convierte automáticamente en `wss` y `http` en `ws`. Cambiar esta variable pública requiere reconstruir el frontend desplegado.
- Backend: `CHAT_ALLOWED_ORIGINS` contiene orígenes exactos separados por comas, por ejemplo `https://app.example.com,capacitor://localhost`. Se aceptan el mismo origen del backend y los orígenes locales de desarrollo de puerto 3000. Los orígenes de previews y Capacitor deben agregarse explícitamente.
- `CHAT_DATABASE_URL` es opcional y permite usar una conexión PostgreSQL directa dedicada a LISTEN. Si falta, se usa `DATABASE_URL`; para URLs Neon con `-pooler.` se deriva el endpoint directo quitando ese sufijo. Con otros poolers de transacciones hay que configurar la URL directa: LISTEN necesita una sesión estable, no pooling de transacciones.
- Todas las réplicas deben usar la misma base y los mismos secretos/orígenes. Cada proceso consume una conexión PostgreSQL adicional. No se necesita afinidad de sesión ni Redis. El proceso necesita un hosting que admita conexiones persistentes; las funciones serverless de corta duración no sirven para este endpoint.
- El proxy debe permitir HTTP/1.1 upgrade y reenviar `Upgrade`/`Connection` en `/v1/chats/ws`, mantener la conexión al menos 90 segundos sin cerrar por idle y terminar TLS para `wss`. El heartbeat se envía cada 25 segundos. El servidor Go cierra sus conexiones WebSocket antes del shutdown HTTP.
- El arranque ejecuta `InitSchema` transaccional: agrega el índice cronológico, `message_receipts` y los triggers. Requiere permisos DDL. Los recibos históricos globales se migran solo para chats DIRECT, donde pueden asignarse correctamente al receptor.

## Verificación

`go test ./...` ejecuta pruebas de autorización, validación, autenticación WebSocket, expiración y clientes lentos. La integración es opt-in:

```powershell
$env:CHAT_INTEGRATION_DATABASE_URL = '<URL de una base con el esquema del proyecto>'
go test ./internal/adapters/handlers -run TestChatRealtimePostgresIntegration -v -count=1 -timeout=180s
```

Para usar la `.env` local, se puede definir `CHAT_INTEGRATION_USE_LOCAL_ENV=1` en vez de copiar credenciales. La prueba crea usuarios/conversación temporales y elimina exclusivamente esos UUIDs al terminar. Prueba dos servidores independientes, pestañas, HTTP/WebSocket reales, persistencia, recibos sin respuesta, acceso ajeno, idempotencia y paginación de 57 mensajes. No envía mensajes a usuarios existentes.

## Squads del Matcher

Los chats persistidos `DIRECT`, `TRANSFER` y `CREWS` comparten autorización y transporte. `/chat/squad/[id]` resuelve el chat real mediante `POST /v1/crews/{id}/chat` y reutiliza exactamente la pantalla de `/chat/{id}`. No carga usuarios ni mensajes de ejemplo; las preferencias/swipes locales no autorizan membresía.

`GET /v1/crews/matches` exige JWT y devuelve solo squads `event_match` pertenecientes al usuario, con evento e integrantes reales. `POST /v1/events/{eventId}/swipes` conserva el criterio previo de emparejamiento por pares, pero serializa por evento y guarda squad, chat CREWS, miembros, participantes y swipes reclamados en una transacción. No se agregó un nuevo algoritmo de afinidad o tamaño de squads.

`POST /v1/crews/{id}/chat` obtiene el usuario del JWT y los participantes de `squad_members`. Bajo un bloqueo del squad reutiliza `chat_room_id`, completa el chat inexistente de un squad histórico o agrega participantes faltantes. Las aperturas concurrentes son idempotentes. Un vínculo con otro squad, un chat no CREWS o participantes ajenos devuelve 409 sin ampliar el acceso. No se migran los mensajes locales de demostración ni se crean grupos a partir de IDs mock (`sq1`).

La creación publica después del commit `{ "type": "chat.created", "chat_id": "UUID" }` en el mismo canal PostgreSQL. Cada instancia lo entrega únicamente a los integrantes; la lista de chats y los squads se refrescan por ese evento, sin polling. `GET /v1/chats` y su detalle agregan `name`, `squad_id`, `event_id` cuando están vinculados a un squad. El arranque crea índices de búsqueda por chat, miembro y swipe pendiente, sin nuevas tablas.

La integración adicional `TestMatcherChatPostgresIntegration` usa usuarios y un evento pasado/no destacado de prueba, elimina solo sus UUIDs y verifica match real, WebSocket, lectura sin respuesta, persistencia, concurrencia, reparación histórica y restricciones a terceros. Para ejecutar ambas integraciones:

```powershell
$env:CHAT_INTEGRATION_USE_LOCAL_ENV = '1'
go test ./internal/adapters/handlers -run 'Test(MatcherChat|ChatRealtime)PostgresIntegration' -v -count=1 -timeout=240s
```
