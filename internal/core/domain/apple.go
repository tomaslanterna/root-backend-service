package domain

import "errors"

var (
	// ErrAppleNotConfigured: faltan las credenciales de Sign in with Apple en el servidor.
	ErrAppleNotConfigured = errors.New("sign in with apple is not configured")
	// ErrInvalidAppleToken: el identityToken no es de Apple, venció, es para otra app o el nonce no coincide.
	ErrInvalidAppleToken = errors.New("invalid apple identity token")
)

// AppleClaims son los datos verificados del identityToken de Apple.
type AppleClaims struct {
	// Subject es el identificador fijo de la persona para esta app ("sub"). Es lo que identifica la cuenta.
	Subject string
	// Audience es el client_id para el que se emitió el token: el bundle id en iOS o el Services ID en web y Android.
	Audience      string
	Email         string
	EmailVerified bool
	// PrivateEmail indica que el email es de relay ("Ocultar mi correo", @privaterelay.appleid.com).
	PrivateEmail bool
}

// AppleLoginInput es lo que manda la app después de que la persona aceptó en la hoja de Apple.
type AppleLoginInput struct {
	IdentityToken     string `json:"identityToken"`
	AuthorizationCode string `json:"authorizationCode"`
	// Nonce es el valor original; a Apple se le mandó su SHA-256 en hexadecimal.
	Nonce      string `json:"nonce"`
	GivenName  string `json:"givenName"`
	FamilyName string `json:"familyName"`
}
