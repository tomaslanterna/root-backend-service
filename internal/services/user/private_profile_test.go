package user

import (
	"context"
	"errors"
	"testing"

	"root-backend-service/internal/core/domain"
	"root-backend-service/internal/core/ports"
)

// fakeUsers: usuarios en memoria con seguidores y solicitudes.
type fakeUsers struct {
	ports.UserRepository
	byUsername map[string]*domain.User
	requests   map[[2]string]bool // [requester, target]
}

func newFakeUsers(users ...*domain.User) *fakeUsers {
	f := &fakeUsers{byUsername: map[string]*domain.User{}, requests: map[[2]string]bool{}}
	for _, u := range users {
		f.byUsername[u.Username] = u
	}
	return f
}

func (f *fakeUsers) byID(id string) *domain.User {
	for _, u := range f.byUsername {
		if u.ID == id {
			return u
		}
	}
	return nil
}

func (f *fakeUsers) GetUserByUsername(_ context.Context, username string) (*domain.User, error) {
	if u, ok := f.byUsername[username]; ok {
		return u, nil
	}
	return nil, domain.ErrUserNotFound
}
func (f *fakeUsers) AddFollower(_ context.Context, followerID, followedID string) error {
	target := f.byID(followedID)
	if !contains(target.Followers, followerID) {
		target.Followers = append(target.Followers, followerID)
	}
	return nil
}
func (f *fakeUsers) RemoveFollower(_ context.Context, followerID, followedID string) error {
	target := f.byID(followedID)
	kept := []string{}
	for _, id := range target.Followers {
		if id != followerID {
			kept = append(kept, id)
		}
	}
	target.Followers = kept
	return nil
}
func (f *fakeUsers) SetPrivate(_ context.Context, userID string, isPrivate bool) error {
	f.byID(userID).IsPrivate = isPrivate
	return nil
}
func (f *fakeUsers) CreateFollowRequest(_ context.Context, requesterID, targetID string) error {
	f.requests[[2]string{requesterID, targetID}] = true
	return nil
}
func (f *fakeUsers) DeleteFollowRequest(_ context.Context, requesterID, targetID string) (bool, error) {
	key := [2]string{requesterID, targetID}
	existed := f.requests[key]
	delete(f.requests, key)
	return existed, nil
}
func (f *fakeUsers) HasFollowRequest(_ context.Context, requesterID, targetID string) (bool, error) {
	return f.requests[[2]string{requesterID, targetID}], nil
}
func (f *fakeUsers) ListFollowRequests(_ context.Context, targetID string) ([]domain.FollowListUser, error) {
	out := []domain.FollowListUser{}
	for key := range f.requests {
		if key[1] == targetID {
			out = append(out, domain.FollowListUser{ID: key[0], Username: f.byID(key[0]).Username})
		}
	}
	return out, nil
}

func TestPrivateProfileFollowFlow(t *testing.T) {
	ctx := context.Background()
	ana := &domain.User{ID: "ana", Username: "ana", IsPrivate: true}
	bruno := &domain.User{ID: "bruno", Username: "bruno"}
	repo := newFakeUsers(ana, bruno)
	svc := NewUserService(repo)

	// Seguir un perfil privado manda una solicitud: todavía no ve el contenido.
	status, err := svc.FollowUser(ctx, "bruno", "ana")
	if err != nil || status != domain.FollowStatusRequested {
		t.Fatalf("status %q err %v", status, err)
	}
	if _, canView, _ := svc.CanViewProfile(ctx, "bruno", "ana"); canView {
		t.Fatal("a pending request must not show private content")
	}
	if st, _ := svc.FollowStatus(ctx, "bruno", ana); st != domain.FollowStatusRequested {
		t.Fatalf("follow status %q", st)
	}

	// Al aceptar, pasa a seguir y ve el contenido.
	if err := svc.AcceptFollowRequest(ctx, "ana", "bruno"); err != nil {
		t.Fatal(err)
	}
	if _, canView, _ := svc.CanViewProfile(ctx, "bruno", "ana"); !canView {
		t.Fatal("an accepted follower must see the content")
	}
	if err := svc.AcceptFollowRequest(ctx, "ana", "bruno"); !errors.Is(err, domain.ErrFollowRequestNotFound) {
		t.Fatalf("accepting twice: %v", err)
	}

	// Seguir un perfil público es inmediato.
	if status, _ := svc.FollowUser(ctx, "ana", "bruno"); status != domain.FollowStatusFollowing {
		t.Fatalf("public follow: %q", status)
	}
}

func TestRejectAndUnfollowCancelRequest(t *testing.T) {
	ctx := context.Background()
	ana := &domain.User{ID: "ana", Username: "ana", IsPrivate: true}
	repo := newFakeUsers(ana, &domain.User{ID: "bruno", Username: "bruno"}, &domain.User{ID: "caro", Username: "caro"})
	svc := NewUserService(repo)

	_, _ = svc.FollowUser(ctx, "bruno", "ana")
	if err := svc.RejectFollowRequest(ctx, "ana", "bruno"); err != nil {
		t.Fatal(err)
	}
	if len(ana.Followers) != 0 || len(repo.requests) != 0 {
		t.Fatalf("reject must not follow: followers %v requests %v", ana.Followers, repo.requests)
	}

	_, _ = svc.FollowUser(ctx, "caro", "ana")
	if err := svc.UnfollowUser(ctx, "caro", "ana"); err != nil {
		t.Fatal(err)
	}
	if len(repo.requests) != 0 {
		t.Fatal("unfollowing cancels the pending request")
	}
}

func TestMakingProfilePublicAcceptsPending(t *testing.T) {
	ctx := context.Background()
	ana := &domain.User{ID: "ana", Username: "ana", IsPrivate: true}
	repo := newFakeUsers(ana, &domain.User{ID: "bruno", Username: "bruno"})
	svc := NewUserService(repo)

	_, _ = svc.FollowUser(ctx, "bruno", "ana")
	if err := svc.SetPrivacy(ctx, "ana", false); err != nil {
		t.Fatal(err)
	}
	if ana.IsPrivate || !contains(ana.Followers, "bruno") || len(repo.requests) != 0 {
		t.Fatalf("private %v followers %v requests %v", ana.IsPrivate, ana.Followers, repo.requests)
	}
	if _, canView, _ := svc.CanViewProfile(ctx, "", "ana"); !canView {
		t.Fatal("a public profile is visible without session")
	}
}
