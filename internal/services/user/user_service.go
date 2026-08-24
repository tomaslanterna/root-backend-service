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

func (s *userService) FollowUser(ctx context.Context, currentUserID, targetUsername string) error {
	targetUser, err := s.userRepo.GetUserByUsername(ctx, targetUsername)
	if err != nil {
		return err
	}

	if targetUser.ID == currentUserID {
		return errors.New("you cannot follow yourself")
	}

	return s.userRepo.AddFollower(ctx, currentUserID, targetUser.ID)
}

func (s *userService) UnfollowUser(ctx context.Context, currentUserID, targetUsername string) error {
	targetUser, err := s.userRepo.GetUserByUsername(ctx, targetUsername)
	if err != nil {
		return err
	}

	if targetUser.ID == currentUserID {
		return errors.New("you cannot unfollow yourself")
	}

	return s.userRepo.RemoveFollower(ctx, currentUserID, targetUser.ID)
}

func (s *userService) UpdateUser(ctx context.Context, userID, newUsername, dob, documentID, country string) (*domain.User, error) {
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
