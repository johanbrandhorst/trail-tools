package user

import (
	"context"

	"connectrpc.com/connect/v2"
	"github.com/johanbrandhorst/trail-tools/internal/authn"
	usersv1 "github.com/johanbrandhorst/trail-tools/internal/gen/users/v1"
	"github.com/johanbrandhorst/trail-tools/internal/store"
	"github.com/johanbrandhorst/trail-tools/internal/users"
)

func (s *Service) GetCurrentUser(ctx context.Context, req *usersv1.GetCurrentUserRequest) (*usersv1.GetCurrentUserResponse, error) {
	user, ok := authn.GetUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, "")
	}
	u, err := userToProto(user)
	if err != nil {
		return nil, err
	}
	return usersv1.GetCurrentUserResponse_builder{User: u}.Build(), nil
}

func userToProto(user users.User) (*usersv1.User, error) {
	switch u := user.(type) {
	case *users.PasskeyUser:
		return usersv1.User_builder{
			Id:        store.UUIDToString(user.ID()),
			Name:      u.Name,
			AvatarUrl: AvatarURL(u.AvatarKey),
			Admin:     u.IsAdmin(),
		}.Build(), nil
	default:
		return nil, connect.Errorf(connect.CodeInternal, "unknown user type: %T", u)
	}
}
