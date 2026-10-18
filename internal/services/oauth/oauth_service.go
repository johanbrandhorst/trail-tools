// Package oauth implements the OAuth service, which lets users approve OAuth
// clients and manage their connected clients.
package oauth

import (
	"context"
	"errors"
	"log/slog"

	"connectrpc.com/connect/v2"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/johanbrandhorst/trail-tools/internal/authn"
	"github.com/johanbrandhorst/trail-tools/internal/errreport"
	oauthv1 "github.com/johanbrandhorst/trail-tools/internal/gen/oauth/v1"
	"github.com/johanbrandhorst/trail-tools/internal/gen/oauth/v1/oauthv1connect"
	"github.com/johanbrandhorst/trail-tools/internal/oauth"
	"github.com/johanbrandhorst/trail-tools/internal/store"
	"github.com/johanbrandhorst/trail-tools/internal/users"
	"google.golang.org/protobuf/types/known/timestamppb"
)

var _ oauthv1connect.OAuthServiceHandler = (*Service)(nil)

// AuthorizationServer validates and approves authorization requests.
type AuthorizationServer interface {
	ParseAuthorizationRequest(query string) (*oauth.AuthorizationRequest, error)
	Approve(ctx context.Context, userID pgtype.UUID, req *oauth.AuthorizationRequest) (string, error)
}

// GrantRepository stores the grants of connected clients.
type GrantRepository interface {
	ListOAuthGrants(ctx context.Context, userID pgtype.UUID) ([]*users.OAuthGrant, error)
	DeleteOAuthGrant(ctx context.Context, userID pgtype.UUID, grantID pgtype.UUID) error
}

// Service implements the OAuth service.
type Service struct {
	log    *slog.Logger
	report *errreport.Reporter
	server AuthorizationServer
	grants GrantRepository
}

// NewService creates a new Service.
func NewService(log *slog.Logger, report *errreport.Reporter, server AuthorizationServer, grants GrantRepository) *Service {
	return &Service{
		log:    log,
		report: report,
		server: server,
		grants: grants,
	}
}

// parseRequest parses an authorization request, returning an
// InvalidArgument error if it is invalid.
func (s *Service) parseRequest(query string) (*oauth.AuthorizationRequest, error) {
	req, err := s.server.ParseAuthorizationRequest(query)
	if err != nil {
		return nil, connect.Errorf(connect.CodeInvalidArgument, "invalid authorization request: %v", err).WithCause(err)
	}
	return req, nil
}

func (s *Service) GetAuthorization(ctx context.Context, req *oauthv1.GetAuthorizationRequest) (*oauthv1.GetAuthorizationResponse, error) {
	if _, ok := authn.GetUserID(ctx); !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, "")
	}
	authzReq, err := s.parseRequest(req.GetQuery())
	if err != nil {
		return nil, err
	}
	return oauthv1.GetAuthorizationResponse_builder{
		ClientName:  authzReq.ClientName,
		RedirectUri: authzReq.RedirectURI,
	}.Build(), nil
}

func (s *Service) ApproveAuthorization(ctx context.Context, req *oauthv1.ApproveAuthorizationRequest) (*oauthv1.ApproveAuthorizationResponse, error) {
	user, err := authn.GetSessionUser(ctx)
	if err != nil {
		return nil, err
	}
	userID := user.ID()
	authzReq, err := s.parseRequest(req.GetQuery())
	if err != nil {
		return nil, err
	}
	redirectURI, err := s.server.Approve(ctx, userID, authzReq)
	if err != nil {
		s.report.Report(ctx, "Failed to approve an authorization", err)
		return nil, connect.NewError(connect.CodeInternal, "failed to approve authorization").WithCause(err)
	}
	return oauthv1.ApproveAuthorizationResponse_builder{
		RedirectUri: redirectURI,
	}.Build(), nil
}

func (s *Service) DenyAuthorization(ctx context.Context, req *oauthv1.DenyAuthorizationRequest) (*oauthv1.DenyAuthorizationResponse, error) {
	authzReq, err := s.parseRequest(req.GetQuery())
	if err != nil {
		return nil, err
	}
	return oauthv1.DenyAuthorizationResponse_builder{
		RedirectUri: authzReq.Deny(),
	}.Build(), nil
}

func (s *Service) ListConnections(ctx context.Context, req *oauthv1.ListConnectionsRequest) (*oauthv1.ListConnectionsResponse, error) {
	user, err := authn.GetSessionUser(ctx)
	if err != nil {
		return nil, err
	}
	userID := user.ID()
	grants, err := s.grants.ListOAuthGrants(ctx, userID)
	if err != nil {
		s.report.Report(ctx, "Failed to list connections", err)
		return nil, connect.NewError(connect.CodeInternal, "failed to list connections").WithCause(err)
	}
	connections := make([]*oauthv1.Connection, len(grants))
	for i, grant := range grants {
		b := oauthv1.Connection_builder{
			Id:          store.UUIDToString(grant.ID),
			ClientName:  grant.ClientName,
			RedirectUri: grant.RedirectURI,
			CreateTime:  timestamppb.New(grant.CreateTime.Time),
		}
		if grant.LastUsedTime.Valid {
			b.LastUsedTime = timestamppb.New(grant.LastUsedTime.Time)
		}
		connections[i] = b.Build()
	}
	return oauthv1.ListConnectionsResponse_builder{
		Connections: connections,
	}.Build(), nil
}

func (s *Service) DeleteConnection(ctx context.Context, req *oauthv1.DeleteConnectionRequest) (*oauthv1.DeleteConnectionResponse, error) {
	user, err := authn.GetSessionUser(ctx)
	if err != nil {
		return nil, err
	}
	userID := user.ID()
	grantID, err := store.StringToUUID(req.GetId())
	if err != nil {
		return nil, connect.Errorf(connect.CodeInvalidArgument, "invalid connection ID: %v", err).WithCause(err)
	}
	if err := s.grants.DeleteOAuthGrant(ctx, userID, grantID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, connect.NewError(connect.CodeNotFound, "connection not found")
		}
		s.report.Report(ctx, "Failed to delete a connection", err)
		return nil, connect.NewError(connect.CodeInternal, "failed to delete connection").WithCause(err)
	}
	return oauthv1.DeleteConnectionResponse_builder{}.Build(), nil
}
