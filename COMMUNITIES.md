# Comunidades

Se reutilizan comunidades, membresías, managers RRPP, publicaciones y comentarios. InitSchema aplica cambios idempotentes en una transacción protegida por advisory lock.

Membresías: `muted`, `last_read_at`, `last_read_post_id`. Lectura monotónica por `(timestamp,id)`; no marca publicaciones que llegaron después del último anuncio cargado. Silencio controla avisos de novedades dentro de la app; no añade push de anuncios ni silencia chats privados.

Publicaciones: `is_pinned`, índice de orden de comunidad/fijado/fecha/ID. La API pagina fijados primero. Solo ADMIN, propietario RRPP o manager RRPP asignado pueden fijar y revisar reportes. El contacto público usa el propietario RRPP o, en su ausencia, un manager RRPP.

Única tabla nueva: `community_reports`, con target polimórfico validado contra publicaciones/comentarios de esa comunidad, snapshot del texto, motivo, detalle, reportante, estado y auditoría de revisión. Restricción de un reporte por usuario/contenido. Los miembros no tienen acceso al listado privado de reportes. Revisar no elimina contenido.

Endpoints (JWT salvo listado/detalle):

- GET `/v1/communities?scope=mine|explore&limit=12&offset=0`, filtros existentes combinables. Mine exige sesión; explore excluye membresías propias. Sin scope conserva compatibilidad.
- PUT `/v1/communities/{id}/membership/preferences` con `{ "muted": true }`.
- POST `/v1/communities/{id}/read` con `{ "postId": "UUID" }`.
- PUT `/v1/communities/{id}/announcements/{postID}/pin` con `{ "pinned": true }`.
- POST `/v1/communities/{id}/reports` con `{ "targetType": "post|comment", "targetId": "UUID", "reason": "spam|abuse|other", "details": "texto opcional" }`.
- GET `/v1/communities/{id}/reports?limit=10&offset=0`, pendientes, solo administración.
- PATCH `/v1/communities/{id}/reports/{reportID}` con `{ "status": "reviewed|dismissed" }`.

JSON estricto; identidad siempre del JWT. 400 validación, 401 sin sesión, 403 permisos, 404 recurso/target ajeno o inexistente, 500 persistencia. Contrato completo compartido en root-web-app/API_Specs.md.

Pruebas: `go test ./...`. Integración aislada opt-in con `COMMUNITY_TEST_DATABASE_URL` o `COMMUNITY_TEST_USE_LOCAL_ENV=1`: `go test ./internal/adapters/repository/postgres -run TestCommunityExperiencePostgresIntegration -v`. Crea un esquema aleatorio, comprueba search_path y elimina únicamente ese esquema. No modifica datos reales.
