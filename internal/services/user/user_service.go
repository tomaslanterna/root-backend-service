package user

import (
	"context"
	"errors"
	"strings"

	"root-backend-service/internal/core/domain"
	"root-backend-service/internal/core/ports"
)

type userService struct {
	userRepo ports.UserRepository
}

func NewUserService(userRepo ports.UserRepository) ports.UserService {
	return &userService{
		userRepo: userRepo,
	}
}

func (s *userService) GetUserByUsername(ctx context.Context, targetUsername string, currentUserID string) (*domain.User, bool, error) {
	user, err := s.userRepo.GetUserByUsername(ctx, targetUsername)
	if err != nil {
		return nil, false, err
	}

	isFollowing := false
	if currentUserID != "" {
		for _, followerID := range user.Followers {
			if followerID == currentUserID {
				isFollowing = true
				break
			}
		}
	}

	return user, isFollowing, nil
}

func (s *userService) FollowUser(ctx context.Context, currentUserID, targetUsername string) (string, error) {
	targetUser, err := s.userRepo.GetUserByUsername(ctx, targetUsername)
	if err != nil {
		return "", err
	}

	if targetUser.ID == currentUserID {
		return "", errors.New("you cannot follow yourself")
	}

	// Perfil privado: se manda una solicitud que la persona acepta o rechaza.
	if targetUser.IsPrivate && !contains(targetUser.Followers, currentUserID) {
		if err := s.userRepo.CreateFollowRequest(ctx, currentUserID, targetUser.ID); err != nil {
			return "", err
		}
		return domain.FollowStatusRequested, nil
	}

	if err := s.userRepo.AddFollower(ctx, currentUserID, targetUser.ID); err != nil {
		return "", err
	}
	return domain.FollowStatusFollowing, nil
}

func contains(list []string, value string) bool {
	for _, item := range list {
		if item == value {
			return true
		}
	}
	return false
}

// FollowStatus: si quien mira sigue al perfil, tiene una solicitud pendiente o ninguna de las dos.
func (s *userService) FollowStatus(ctx context.Context, viewerID string, target *domain.User) (string, error) {
	if viewerID == "" || target == nil || viewerID == target.ID {
		return domain.FollowStatusNone, nil
	}
	if contains(target.Followers, viewerID) {
		return domain.FollowStatusFollowing, nil
	}
	if !target.IsPrivate {
		return domain.FollowStatusNone, nil
	}
	pending, err := s.userRepo.HasFollowRequest(ctx, viewerID, target.ID)
	if err != nil {
		return "", err
	}
	if pending {
		return domain.FollowStatusRequested, nil
	}
	return domain.FollowStatusNone, nil
}

// CanViewProfile: los perfiles públicos los ve cualquiera; los privados, su dueño y sus seguidores.
func (s *userService) CanViewProfile(ctx context.Context, viewerID, targetUsername string) (*domain.User, bool, error) {
	target, err := s.userRepo.GetUserByUsername(ctx, targetUsername)
	if err != nil {
		return nil, false, err
	}
	if !target.IsPrivate || viewerID == target.ID || contains(target.Followers, viewerID) {
		return target, true, nil
	}
	return target, false, nil
}

// SetPrivacy cambia el perfil entre público y privado. Al volverlo público, las solicitudes pendientes
// pasan a ser seguidores (ya no hay nada que aprobar).
func (s *userService) SetPrivacy(ctx context.Context, userID string, isPrivate bool) error {
	if err := s.userRepo.SetPrivate(ctx, userID, isPrivate); err != nil {
		return err
	}
	if isPrivate {
		return nil
	}
	pending, err := s.userRepo.ListFollowRequests(ctx, userID)
	if err != nil {
		return err
	}
	for _, requester := range pending {
		if err := s.userRepo.AddFollower(ctx, requester.ID, userID); err != nil {
			return err
		}
		if _, err := s.userRepo.DeleteFollowRequest(ctx, requester.ID, userID); err != nil {
			return err
		}
	}
	return nil
}

// ListFollowRequests: quienes pidieron seguir a userID.
func (s *userService) ListFollowRequests(ctx context.Context, userID string) ([]domain.FollowListUser, error) {
	return s.userRepo.ListFollowRequests(ctx, userID)
}

// AcceptFollowRequest convierte la solicitud de requesterUsername en seguimiento.
func (s *userService) AcceptFollowRequest(ctx context.Context, userID, requesterUsername string) error {
	requester, err := s.userRepo.GetUserByUsername(ctx, requesterUsername)
	if err != nil {
		return err
	}
	existed, err := s.userRepo.DeleteFollowRequest(ctx, requester.ID, userID)
	if err != nil {
		return err
	}
	if !existed {
		return domain.ErrFollowRequestNotFound
	}
	return s.userRepo.AddFollower(ctx, requester.ID, userID)
}

// RejectFollowRequest borra la solicitud de requesterUsername sin seguir.
func (s *userService) RejectFollowRequest(ctx context.Context, userID, requesterUsername string) error {
	requester, err := s.userRepo.GetUserByUsername(ctx, requesterUsername)
	if err != nil {
		return err
	}
	existed, err := s.userRepo.DeleteFollowRequest(ctx, requester.ID, userID)
	if err != nil {
		return err
	}
	if !existed {
		return domain.ErrFollowRequestNotFound
	}
	return nil
}

func (s *userService) UnfollowUser(ctx context.Context, currentUserID, targetUsername string) error {
	targetUser, err := s.userRepo.GetUserByUsername(ctx, targetUsername)
	if err != nil {
		return err
	}

	if targetUser.ID == currentUserID {
		return errors.New("you cannot unfollow yourself")
	}

	// Dejar de seguir también cancela una solicitud pendiente.
	if _, err := s.userRepo.DeleteFollowRequest(ctx, currentUserID, targetUser.ID); err != nil {
		return err
	}
	return s.userRepo.RemoveFollower(ctx, currentUserID, targetUser.ID)
}

// GetFollowers lista quienes siguen a targetUsername.
func (s *userService) GetFollowers(ctx context.Context, targetUsername, viewerID string, limit, offset int) ([]domain.FollowListUser, int, error) {
	target, err := s.userRepo.GetUserByUsername(ctx, targetUsername)
	if err != nil {
		return nil, 0, err
	}
	return s.userRepo.ListFollowers(ctx, target.ID, viewerID, limit, offset)
}

// GetFollowing lista a quienes sigue targetUsername.
func (s *userService) GetFollowing(ctx context.Context, targetUsername, viewerID string, limit, offset int) ([]domain.FollowListUser, int, error) {
	target, err := s.userRepo.GetUserByUsername(ctx, targetUsername)
	if err != nil {
		return nil, 0, err
	}
	return s.userRepo.ListFollowing(ctx, target.ID, viewerID, limit, offset)
}

// GetInterests devuelve los intereses musicales y si ya respondió la pregunta.
func (s *userService) GetInterests(ctx context.Context, userID string) ([]string, bool, error) {
	return s.userRepo.GetInterests(ctx, userID)
}

// SetInterests valida y guarda los intereses musicales; devuelve la lista guardada (nombres canónicos).
func (s *userService) SetInterests(ctx context.Context, userID string, genres []string) ([]string, error) {
	clean, err := domain.NormalizeInterests(genres)
	if err != nil {
		return nil, err
	}
	if err := s.userRepo.SetInterests(ctx, userID, clean); err != nil {
		return nil, err
	}
	return clean, nil
}

// UpdateAvatar guarda la nueva foto de perfil (URL estable o clave de S3) y devuelve el usuario actualizado.
func (s *userService) UpdateAvatar(ctx context.Context, userID, avatar string) (*domain.User, error) {
	user, err := s.userRepo.GetUserByID(ctx, userID)
	if err != nil {
		return nil, err
	}
	if err := s.userRepo.UpdateAvatar(ctx, userID, avatar); err != nil {
		return nil, err
	}
	user.AvatarURL = &avatar
	return user, nil
}

func (s *userService) UpdateUser(ctx context.Context, userID, name, newUsername, dob, documentID, country string) (*domain.User, error) {
	// 1. Get current user
	user, err := s.userRepo.GetUserByID(ctx, userID)
	if err != nil {
		return nil, err
	}

	// 2. Check if new username is taken by someone else
	if newUsername != "" {
		newUsername = strings.ToLower(newUsername)
	}
	if newUsername != "" && newUsername != user.Username {
		existingUser, err := s.userRepo.GetUserByUsername(ctx, newUsername)
		if err == nil && existingUser != nil {
			return nil, errors.New("username is already taken")
		}
		user.Username = newUsername
	}

	// 3. Update fields
	// El nombre es opcional en el pedido, pero si viene tiene que tener nombre y apellido.
	if name != "" {
		name = domain.NormalizeFullName(name)
		if !domain.IsFullName(name) {
			return nil, domain.ErrInvalidFullName
		}
		user.Name = name
	}
	if dob != "" {
		user.Dob = &dob
	}
	if documentID != "" {
		user.DocumentID = &documentID
	}
	if country != "" {
		user.Country = &country
	}

	// 4. Save to DB
	if err := s.userRepo.UpdateUser(ctx, user); err != nil {
		return nil, err
	}

	return user, nil
}
