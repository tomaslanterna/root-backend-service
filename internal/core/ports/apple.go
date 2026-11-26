package ports

import (
	"context"

	"root-backend-service/internal/core/domain"
)

// AppleIdentityRepository vincula cuentas de root con su identificador de Apple (tabla apple_identities).
type AppleIdentityRepository interface {
	// UserIDBySubject devuelve la cuenta vinculada a ese "sub" de Apple; domain.ErrUserNotFound si no hay ninguna.
	UserIDBySubject(ctx context.Context, subject string) (string, error)
	// Link vincula la cuenta con el "sub". Si la cuenta ya estaba vinculada, actualiza los datos.
	Link(ctx context.Context, userID, subject, clientID, email string) error
	// SetRefreshToken guarda el refresh token de Apple, que se usa para revocar el acceso al borrar la cuenta.
	SetRefreshToken(ctx context.Context, userID, clientID, refreshToken string) error
	// RefreshToken devuelve el refresh token y el client_id con el que se obtuvo; "" si no hay.
	RefreshToken(ctx context.Context, userID string) (refreshToken, clientID string, err error)
}

// AppleAuthClient habla con Apple: verifica identityTokens, canjea el authorization code y revoca accesos.
type AppleAuthClient interface {
	// Verify valida firma, emisor, vencimiento, audiencia y nonce (nonceHash es el SHA-256 en hex del nonce).
	Verify(ctx context.Context, identityToken, nonceHash string) (domain.AppleClaims, error)
	// Exchange canjea el authorization code por un refresh token.
	Exchange(ctx context.Context, authorizationCode, clientID string) (refreshToken string, err error)
	// Revoke revoca el refresh token: Apple corta el vínculo de la persona con la app.
	Revoke(ctx context.Context, refreshToken, clientID string) error
}
