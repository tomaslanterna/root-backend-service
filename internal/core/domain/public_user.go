package domain

// PublicUser es lo que cualquiera puede ver de otro usuario en resultados de búsqueda.
// Nunca incluye email, fecha de nacimiento, documento, rol ni las listas de IDs de seguidores.
type PublicUser struct {
	ID            string  `json:"id"`
	Name          string  `json:"name"`
	Username      string  `json:"username"`
	AvatarURL     *string `json:"avatarUrl"`
	IsKycVerified bool    `json:"isKycVerified"`
}

// PublicProfile es el perfil de otro usuario (GET /v1/users/{username} cuando no es el dueño).
// Followers/Following se mantienen porque el front actual cuenta con su largo y los actualiza
// al seguir/dejar de seguir; los conteos permiten migrar el front y luego quitarlas.
type PublicProfile struct {
	ID             string   `json:"id"`
	Name           string   `json:"name"`
	Username       string   `json:"username"`
	Role           string   `json:"role"`
	AvatarURL      *string  `json:"avatarUrl"`
	IsKycVerified  bool     `json:"isKycVerified"`
	Followers      []string `json:"followers"`
	Following      []string `json:"following"`
	FollowersCount int      `json:"followersCount"`
	FollowingCount int      `json:"followingCount"`
}

func (u User) Public() PublicUser {
	return PublicUser{
		ID:            u.ID,
		Name:          u.Name,
		Username:      u.Username,
		AvatarURL:     u.AvatarURL,
		IsKycVerified: u.IsKycVerified,
	}
}

func PublicUsers(users []User) []PublicUser {
	out := make([]PublicUser, 0, len(users))
	for _, u := range users {
		out = append(out, u.Public())
	}
	return out
}

func (u User) PublicProfile() PublicProfile {
	followers, following := u.Followers, u.Following
	if followers == nil {
		followers = []string{}
	}
	if following == nil {
		following = []string{}
	}
	return PublicProfile{
		ID:             u.ID,
		Name:           u.Name,
		Username:       u.Username,
		Role:           u.Role,
		AvatarURL:      u.AvatarURL,
		IsKycVerified:  u.IsKycVerified,
		Followers:      followers,
		Following:      following,
		FollowersCount: len(followers),
		FollowingCount: len(following),
	}
}
