# Recuperación de contraseña

## Proveedor elegido: Resend

Se eligió Resend para los emails de recuperación. La integración está implementada y requiere configurar las credenciales privadas del entorno y verificar un dominio/remitente para enviar a usuarios reales. No se activa el worker ni se simulan envíos mientras falten esas credenciales. El adaptador alternativo de Brevo queda disponible, pero no se utiliza.

1. Crear una cuenta en https://resend.com.
2. En Domains, agregar un dominio propio (o subdominio) y cargar los registros DNS indicados por Resend. No usar el dominio compartido de Vercel como dominio remitente.
3. Esperar que figure Verified; dejar desactivado el tracking de enlaces para recuperación.
4. En API Keys, crear una key con permiso Sending access, limitada al dominio verificado.
5. Configurar las variables privadas siguientes en `.env` local del backend y, al desplegar, en los secretos del hosting. `.env` está excluido de Git; no pegar la key en chats ni usarla en el frontend.
6. Reiniciar backend y probar con una cuenta propia de prueba que tenga contraseña.

Para probar antes de verificar un dominio, Resend permite usar `Root <onboarding@resend.dev>` únicamente para el email asociado a la cuenta Resend. Es una restricción de pruebas, no una configuración apta para usuarios reales.

Variables privadas del backend (nunca `NEXT_PUBLIC_*`):

```dotenv
PASSWORD_RESET_EMAIL_PROVIDER=resend
PASSWORD_RESET_EMAIL_API_KEY=<API key privada de Resend>
PASSWORD_RESET_EMAIL_FROM="Root <recuperacion@tu-dominio-verificado.com>"
PASSWORD_RESET_TOKEN_KEY=<secreto aleatorio independiente de al menos 32 bytes>
FRONTEND_URL=https://tu-frontend.example.com
```

Sin proveedor configurado, ambos endpoints responden 503; no se simula un envío. Configuración incompleta o inválida impide arrancar para evitar un servicio aparentemente operativo. No guardar secretos en Git. Generar la clave con un gestor de secretos/CSPRNG y conservarla estable entre instancias y reinicios: rotarla invalida los enlaces existentes y los reintentos pendientes. No reutilizar `JWT_SECRET` ni claves del proveedor.

`FRONTEND_URL` debe ser el origen del frontend y usar HTTPS. Para desarrollo admite `http://localhost:3000` (Android necesita USB reverse de 3000/8080), o `http://127.0.0.1:3000`. No se construyen enlaces desde Host/Origin enviados por el cliente. El enlace abre la web responsive; no se agregaron App Links/deep links nativos.

Documentación de APIs: [Resend](https://resend.com/docs/api-reference/emails/send-email), [Brevo](https://developers.brevo.com/reference/send-transac-email). Desactivar tracking de enlaces en el proveedor para evitar reescrituras de enlaces sensibles. Usar remitente autorizado para destinatarios reales, no un remitente de prueba restringido.

## Contrato

- `POST /v1/auth/forgot-password`: `{ "email": "..." }`. 202 con el mismo mensaje para cuentas con contraseña, inexistentes, exclusivamente Google y emails ambiguos al normalizar mayúsculas/minúsculas.
- `POST /v1/auth/reset-password`: `{ "token": "...", "password": "...", "confirmPassword": "..." }`. 200 al completar el cambio. Solo el token autoriza; `email`, `userId` y campos adicionales se rechazan.
- Ambos: 400 validación, 429 límite, 503 sin configuración, 500 fallo interno. JSON limitado a 4096 bytes; `Cache-Control: no-store`.

202 confirma **la solicitud encolada**, no un correo enviado/entregado. El frontend lo dice explícitamente. El worker activa el token solo tras recibir un ID de aceptación del proveedor; no equivale a recepción en inbox (spam/bounces requieren revisar el proveedor). Nunca retorna tokens ni enlaces como reemplazo del email. Un fallo de envío deja el token inactivo y registra un reintento, no un envío exitoso. Mantener una respuesta asíncrona común evita revelar cuentas mediante errores específicos de entrega.

## Persistencia, seguridad y operación

`InitSchema` transaccional/idempotente agrega `users.session_version`, `password_reset_deliveries` e `auth_rate_limits`, con índices y constraints. No duplica usuarios ni almacenamiento de contraseñas.

Un nonce CSPRNG de 32 bytes y HMAC-SHA256 con la clave privada producen el token. Solo se persiste su hash SHA256; el nonce interno permite reconstruir el mismo envío para reintentos idempotentes sin guardar el token en claro. No exponer IDs internos de entregas. Vence a los 30 minutos de la solicitud. El token viaja en el fragmento `/reset-password#token=...`: no llega a logs de Next ni referrers; al confirmar se envía exclusivamente en el body del POST. La página usa no-referrer/noindex. No registrar bodies en proxies, APM ni proveedores.

El cambio bloquea al usuario y revalida token/versión/vencimiento dentro de la transacción: consume todos sus enlaces, actualiza bcrypt e incrementa la versión. Solicitudes concurrentes con el mismo token o diferentes tokens del usuario solo pueden completar un cambio. Registro y recuperación comparten mínimo 8 caracteres Unicode y máximo 72 bytes UTF-8 (límite bcrypt). Login mantiene compatibilidad con contraseñas anteriores más cortas.

JWT anteriores sin versión se interpretan como versión 0. Funcionan hasta el primer reset; después quedan invalidados por la comprobación central de versión en HTTP y nuevas conexiones WebSocket. WebSockets abiertos revalidan en su heartbeat existente (hasta 25 segundos); no se crea otro provider/conexión. Se eliminan los dispositivos push de sesiones revocadas y sus jobs pendientes por cascade; el usuario deberá habilitar notificaciones nuevamente tras iniciar sesión. Google sigue funcionando; una cuenta exclusivamente OAuth no recibe reset para no convertirla silenciosamente en una cuenta con contraseña.

Límites persistidos y atómicos: solicitud por IP 10/15 minutos, por email 3/hora; validación por IP 10/15 minutos y por token 5/15 minutos. Las claves de límite se guardan hasheadas. Se usa RemoteAddr y se ignora X-Forwarded-For arbitrario: detrás de reverse proxy, normalizar IP solo desde proxies de confianza (si no, comparten cuota los clientes del proxy). Retry-After genérico de 900 segundos es orientativo; la cuota por email puede durar una hora.

Worker cada 2 segundos, hasta 10 entregas por pasada. Claim con `FOR UPDATE SKIP LOCKED`, lease de 2 minutos, máximo 5 intentos y backoff 1/2/4/8 minutos dentro del TTL. Estados `pending`, `processing`, `sent` (aceptado por proveedor), `ignored`, `failed`, `consumed`. Logs sanitizados, sin emails/tokens/URLs/API keys. Para diagnosticar sin exponer PII:

```sql
SELECT status, COUNT(*) FROM password_reset_deliveries
WHERE created_at >= NOW() - INTERVAL '1 hour' GROUP BY status;
```

El worker elimina en lotes acotados entregas vencidas y cuotas que llevan un día expiradas. Si se desactiva el proveedor, también se detiene ese mantenimiento; programar limpieza operativa si se mantiene desactivado prolongadamente. No hay endpoint público de estado individual (evita enumeración).

## Verificación

`go test ./...` ejecuta pruebas unitarias de autenticación, HTTP, límites, política y transporte email. Para PostgreSQL: `AUTH_TEST_DATABASE_URL=<DB de pruebas>` o `AUTH_TEST_USE_LOCAL_ENV=1 go test ./...` lee DATABASE_URL local sin imprimirla. La integración crea un esquema `auth_test_<uuid>`, verifica search_path en dos conexiones independientes y elimina únicamente ese esquema al finalizar. Prueba migración repetida, OAuth/inexistentes, backoff, token inactivo/no enviado, concurrencia, expiración, consumo, revocación, persistencia de contraseña/login y cuotas concurrentes. Nunca modifica usuarios reales ni envía emails.

Se probó la recuperación con Resend y su remitente de pruebas en un entorno aislado, sin modificar la cuenta real de Google. El usuario confirmó que funcionó; el entorno temporal fue retirado y no forma parte del proyecto.

Para verificar en producción, configurar un dominio remitente propio, reiniciar backend y solicitar un enlace con una cuenta de prueba propia con contraseña. Abrir el correo, cambiar contraseña, comprobar que la anterior no entra y el enlace ya no sirve. Repetir en navegador/Android; verificar spam, errores del proveedor y expiración. La validación con destinatarios arbitrarios y la prueba nativa Android siguen pendientes.
