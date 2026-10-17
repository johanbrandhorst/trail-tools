package user

import (
	"context"
	"errors"
	"strings"

	"connectrpc.com/connect/v2"
	"github.com/johanbrandhorst/trail-tools/internal/authn"
	usersv1 "github.com/johanbrandhorst/trail-tools/internal/gen/users/v1"
	"github.com/johanbrandhorst/trail-tools/internal/store"
	"github.com/johanbrandhorst/trail-tools/internal/users"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func (s *Service) CreateAPIToken(ctx context.Context, req *usersv1.CreateAPITokenRequest) (*usersv1.CreateAPITokenResponse, error) {
	user, err := authn.GetSessionUser(ctx)
	if err != nil {
		return nil, err
	}
	userID := user.ID()
	name := strings.TrimSpace(req.GetName())
	if name == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, "name is required")
	}
	token, secret, err := s.tokens.CreateAPIToken(ctx, userID, name)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, "failed to create API token").WithCause(err)
	}
	return usersv1.CreateAPITokenResponse_builder{
		Token:  apiTokenToProto(token),
		Secret: secret,
	}.Build(), nil
}

func (s *Service) ListAPITokens(ctx context.Context, req *usersv1.ListAPITokensRequest) (*usersv1.ListAPITokensResponse, error) {
	user, err := authn.GetSessionUser(ctx)
	if err != nil {
		return nil, err
	}
	userID := user.ID()
	tokens, err := s.tokens.ListAPITokens(ctx, userID)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, "failed to list API tokens").WithCause(err)
	}
	protoTokens := make([]*usersv1.APIToken, len(tokens))
	for i, token := range tokens {
		protoTokens[i] = apiTokenToProto(token)
	}
	return usersv1.ListAPITokensResponse_builder{
		Tokens: protoTokens,
	}.Build(), nil
}

func (s *Service) DeleteAPIToken(ctx context.Context, req *usersv1.DeleteAPITokenRequest) (*usersv1.DeleteAPITokenResponse, error) {
	user, err := authn.GetSessionUser(ctx)
	if err != nil {
		return nil, err
	}
	userID := user.ID()
	tokenID, err := store.StringToUUID(req.GetId())
	if err != nil {
		return nil, connect.Errorf(connect.CodeInvalidArgument, "invalid token ID: %v", err).WithCause(err)
	}
	if err := s.tokens.DeleteAPIToken(ctx, userID, tokenID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, connect.NewError(connect.CodeNotFound, "token not found")
		}
		return nil, connect.NewError(connect.CodeInternal, "failed to delete API token").WithCause(err)
	}
	return usersv1.DeleteAPITokenResponse_builder{}.Build(), nil
}

func apiTokenToProto(token *users.APIToken) *usersv1.APIToken {
	b := usersv1.APIToken_builder{
		Id:         store.UUIDToString(token.ID),
		Name:       token.Name,
		CreateTime: timestamppb.New(token.CreateTime.Time),
	}
	if token.LastUsedTime.Valid {
		b.LastUsedTime = timestamppb.New(token.LastUsedTime.Time)
	}
	return b.Build()
}
