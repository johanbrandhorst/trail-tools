// Package auth implements the AuthService: passkey registration and sign in,
// and invitations.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/go-passkeys/go-passkeys/webauthn"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/johanbrandhorst/trail-tools/internal/authn"
	"github.com/johanbrandhorst/trail-tools/internal/errreport"
	authv1 "github.com/johanbrandhorst/trail-tools/internal/gen/auth/v1"
	"github.com/johanbrandhorst/trail-tools/internal/gen/auth/v1/authv1connect"
	"github.com/johanbrandhorst/trail-tools/internal/html"
	"github.com/johanbrandhorst/trail-tools/internal/store"
	"github.com/johanbrandhorst/trail-tools/internal/users"
	"gitlab.com/greyxor/slogor"
	"google.golang.org/protobuf/types/known/timestamppb"
)

var _ authv1connect.AuthServiceHandler = (*Service)(nil)

const (
	// challengeTTL is how long a browser has to complete a ceremony.
	challengeTTL = 5 * time.Minute
	// invitationTTL is how long invitations are valid.
	invitationTTL = 7 * 24 * time.Hour
	// sessionTTL is how long users stay signed in.
	sessionTTL = 30 * 24 * time.Hour
	// maxNameLength limits user names and invitation notes.
	maxNameLength = 100
	// InvitePath is the frontend page for invitations. Their links carry
	// the token in the fragment, which browsers don't send, so it doesn't
	// end up in request logs.
	InvitePath = "/invite"
)

// Repository stores users, passkeys and invitations.
type Repository interface {
	HasAdmin(ctx context.Context) (bool, error)
	GetUser(ctx context.Context, userID pgtype.UUID) (users.User, error)
	GetPasskeyUser(ctx context.Context, userID pgtype.UUID) (*users.PasskeyUser, error)
	CreateChallenge(ctx context.Context, c *users.Challenge, expiry time.Time) (pgtype.UUID, error)
	TakeChallenge(ctx context.Context, id pgtype.UUID, ceremony users.Ceremony) (*users.Challenge, error)
	CreateInvitation(ctx context.Context, createdBy pgtype.UUID, kind users.InvitationKind, userID pgtype.UUID, note string, expiry time.Time) (*users.Invitation, string, error)
	GetInvitationByToken(ctx context.Context, token string) (*users.Invitation, error)
	ListInvitations(ctx context.Context, createdBy pgtype.UUID) ([]*users.Invitation, error)
	DeleteInvitation(ctx context.Context, createdBy, id pgtype.UUID) error
	ListPasskeys(ctx context.Context, userID pgtype.UUID) ([]*users.Passkey, error)
	ListPasskeyIDs(ctx context.Context, userID pgtype.UUID) ([][]byte, error)
	GetPasskey(ctx context.Context, credentialID []byte) (*users.Passkey, error)
	UsePasskey(ctx context.Context, credentialID []byte, signCount uint32, backedUp bool) error
	DeletePasskey(ctx context.Context, userID pgtype.UUID, credentialID []byte) error
	RegisterPasskey(ctx context.Context, reg *users.Registration) (users.User, error)
	CreateSession(ctx context.Context, userID pgtype.UUID, expiry time.Time) (string, error)
}

// Service implements API handlers for the auth service.
type Service struct {
	log            *slog.Logger
	report         *errreport.Reporter
	repo           Repository
	rp             *webauthn.RelyingParty
	rpName         string
	baseURL        string
	bootstrapToken string
	nowFn          func() time.Time
}

// NewService creates a new Service. baseURL is the public URL of the
// application, which determines the WebAuthn relying party. bootstrapToken,
// if set, works as an invitation to create the first administrator while
// there are no administrators.
func NewService(log *slog.Logger, report *errreport.Reporter, repo Repository, baseURL, bootstrapToken string) (*Service, error) {
	u, err := url.Parse(baseURL)
	if err != nil || u.Host == "" {
		return nil, fmt.Errorf("invalid base URL %q", baseURL)
	}
	return &Service{
		log:    log,
		report: report,
		repo:   repo,
		rp: &webauthn.RelyingParty{
			ID:     u.Hostname(),
			Origin: u.Scheme + "://" + u.Host,
		},
		rpName:         "trailrunn.ing",
		baseURL:        strings.TrimSuffix(baseURL, "/"),
		bootstrapToken: bootstrapToken,
		nowFn:          time.Now,
	}, nil
}

func (s *Service) GetInvitation(ctx context.Context, req *authv1.GetInvitationRequest) (*authv1.GetInvitationResponse, error) {
	token := req.GetToken()
	bootstrap, err := s.isBootstrap(ctx, token)
	if err != nil {
		return nil, s.internalError(ctx, "failed to check bootstrap token", err)
	}
	if bootstrap {
		return authv1.GetInvitationResponse_builder{
			Invitation: authv1.Invitation_builder{
				Kind:      authv1.InvitationKind_INVITATION_KIND_NEW_USER,
				Note:      "Create the administrator account",
				Bootstrap: true,
			}.Build(),
		}.Build(), nil
	}
	inv, err := s.repo.GetInvitationByToken(ctx, token)
	if err != nil {
		return nil, s.toConnectError(ctx, err, "failed to get invitation")
	}
	return authv1.GetInvitationResponse_builder{Invitation: invitationToProto(inv)}.Build(), nil
}

func (s *Service) CreateInvitation(ctx context.Context, req *authv1.CreateInvitationRequest) (*authv1.CreateInvitationResponse, error) {
	user, err := authn.GetSessionUser(ctx)
	if err != nil {
		return nil, err
	}
	note := strings.TrimSpace(req.GetNote())
	if len(note) > maxNameLength {
		return nil, connect.Errorf(connect.CodeInvalidArgument, "note must be at most %d characters", maxNameLength)
	}
	var kind users.InvitationKind
	var target pgtype.UUID
	switch req.GetKind() {
	case authv1.InvitationKind_INVITATION_KIND_NEW_USER:
		if !user.IsAdmin() {
			return nil, connect.NewError(connect.CodePermissionDenied, "only administrators can invite new users")
		}
		kind = users.InvitationNewUser
	case authv1.InvitationKind_INVITATION_KIND_ADD_PASSKEY:
		kind, target = users.InvitationAddPasskey, user.ID()
	default:
		return nil, connect.NewError(connect.CodeInvalidArgument, "kind is required")
	}
	inv, token, err := s.repo.CreateInvitation(ctx, user.ID(), kind, target, note, s.nowFn().Add(invitationTTL))
	if err != nil {
		return nil, s.internalError(ctx, "failed to create invitation", err)
	}
	return authv1.CreateInvitationResponse_builder{
		Invitation: invitationToProto(inv),
		Url:        s.baseURL + html.TokenURL(InvitePath, token),
	}.Build(), nil
}

func (s *Service) ListInvitations(ctx context.Context, req *authv1.ListInvitationsRequest) (*authv1.ListInvitationsResponse, error) {
	user, err := authn.GetSessionUser(ctx)
	if err != nil {
		return nil, err
	}
	userID := user.ID()
	invitations, err := s.repo.ListInvitations(ctx, userID)
	if err != nil {
		return nil, s.internalError(ctx, "failed to list invitations", err)
	}
	protoInvitations := make([]*authv1.Invitation, len(invitations))
	for i, inv := range invitations {
		protoInvitations[i] = invitationToProto(inv)
	}
	return authv1.ListInvitationsResponse_builder{Invitations: protoInvitations}.Build(), nil
}

func (s *Service) DeleteInvitation(ctx context.Context, req *authv1.DeleteInvitationRequest) (*authv1.DeleteInvitationResponse, error) {
	user, err := authn.GetSessionUser(ctx)
	if err != nil {
		return nil, err
	}
	userID := user.ID()
	id, err := store.StringToUUID(req.GetId())
	if err != nil {
		return nil, connect.Errorf(connect.CodeInvalidArgument, "invalid invitation ID: %v", err).WithCause(err)
	}
	if err := s.repo.DeleteInvitation(ctx, userID, id); err != nil {
		return nil, s.toConnectError(ctx, err, "failed to delete invitation")
	}
	return authv1.DeleteInvitationResponse_builder{}.Build(), nil
}

func (s *Service) ListPasskeys(ctx context.Context, req *authv1.ListPasskeysRequest) (*authv1.ListPasskeysResponse, error) {
	user, err := authn.GetSessionUser(ctx)
	if err != nil {
		return nil, err
	}
	userID := user.ID()
	passkeys, err := s.repo.ListPasskeys(ctx, userID)
	if err != nil {
		return nil, s.internalError(ctx, "failed to list passkeys", err)
	}
	protoPasskeys := make([]*authv1.Passkey, len(passkeys))
	for i, p := range passkeys {
		b := authv1.Passkey_builder{
			Id:         base64.RawURLEncoding.EncodeToString(p.ID),
			Name:       p.Name,
			BackedUp:   p.BackedUp,
			CreateTime: timestamppb.New(p.CreateTime),
		}
		if !p.LastUsedTime.IsZero() {
			b.LastUsedTime = timestamppb.New(p.LastUsedTime)
		}
		protoPasskeys[i] = b.Build()
	}
	return authv1.ListPasskeysResponse_builder{Passkeys: protoPasskeys}.Build(), nil
}

func (s *Service) DeletePasskey(ctx context.Context, req *authv1.DeletePasskeyRequest) (*authv1.DeletePasskeyResponse, error) {
	user, err := authn.GetSessionUser(ctx)
	if err != nil {
		return nil, err
	}
	userID := user.ID()
	id, err := base64.RawURLEncoding.DecodeString(req.GetId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, "invalid passkey ID")
	}
	if err := s.repo.DeletePasskey(ctx, userID, id); err != nil {
		return nil, s.toConnectError(ctx, err, "failed to delete passkey")
	}
	return authv1.DeletePasskeyResponse_builder{}.Build(), nil
}

func (s *Service) BeginPasskeyRegistration(ctx context.Context, req *authv1.BeginPasskeyRegistrationRequest) (*authv1.BeginPasskeyRegistrationResponse, error) {
	challenge := &users.Challenge{Ceremony: users.CeremonyRegistration, Challenge: randomBytes(32)}
	var target pgtype.UUID
	token := req.GetInvitationToken()
	bootstrap, err := s.isBootstrap(ctx, token)
	if err != nil {
		return nil, s.internalError(ctx, "failed to check bootstrap token", err)
	}
	switch {
	case bootstrap:
		challenge.Bootstrap = true
	case token != "":
		inv, err := s.repo.GetInvitationByToken(ctx, token)
		if err != nil {
			return nil, s.toConnectError(ctx, err, "failed to get invitation")
		}
		challenge.InvitationID = inv.ID
		if inv.Kind == users.InvitationAddPasskey {
			target = inv.UserID
		}
	default:
		// Adding a passkey to the account signs in with it, so only the
		// web app may do it, not API clients with a bearer token.
		if _, ok := authn.GetUserID(ctx); !ok {
			return nil, connect.NewError(connect.CodeUnauthenticated, "an invitation is required to register")
		}
		user, err := authn.GetSessionUser(ctx)
		if err != nil {
			return nil, err
		}
		challenge.UserID = user.ID()
		target = user.ID()
	}

	resp := authv1.BeginPasskeyRegistrationResponse_builder{
		Challenge: challenge.Challenge,
		RpId:      s.rp.ID,
		RpName:    s.rpName,
	}
	if target.Valid {
		// Use the existing user's handle, so authenticators know the new
		// passkey is for the same account.
		challenge.UserHandle, resp.UserName, err = s.userHandle(ctx, target)
		if err != nil {
			return nil, err
		}
		if resp.ExcludeCredentialIds, err = s.repo.ListPasskeyIDs(ctx, target); err != nil {
			return nil, s.internalError(ctx, "failed to list passkeys", err)
		}
	} else {
		challenge.UserHandle = randomBytes(32)
	}
	resp.UserHandle = challenge.UserHandle
	id, err := s.repo.CreateChallenge(ctx, challenge, s.nowFn().Add(challengeTTL))
	if err != nil {
		return nil, s.internalError(ctx, "failed to create challenge", err)
	}
	resp.ChallengeId = store.UUIDToString(id)
	return resp.Build(), nil
}

func (s *Service) FinishPasskeyRegistration(ctx context.Context, req *authv1.FinishPasskeyRegistrationRequest) (*authv1.FinishPasskeyRegistrationResponse, error) {
	info, _ := connect.CallInfoForServerContext(ctx)
	challenge, err := s.takeChallenge(ctx, req.GetChallengeId(), users.CeremonyRegistration)
	if err != nil {
		return nil, err
	}
	att, err := s.rp.VerifyAttestation(challenge.Challenge, req.GetClientDataJson(), req.GetAttestationObject())
	if err != nil {
		s.log.DebugContext(ctx, "Invalid passkey registration", slogor.Err(err))
		return nil, connect.NewError(connect.CodeInvalidArgument, "the passkey could not be verified")
	}
	if !att.Flags.UserPresent() || !att.Flags.UserVerified() {
		return nil, connect.NewError(connect.CodeInvalidArgument, "the passkey must verify the user, e.g. with a fingerprint, face or PIN")
	}
	publicKey, err := x509.MarshalPKIXPublicKey(att.PublicKey)
	if err != nil {
		return nil, connect.Errorf(connect.CodeInvalidArgument, "unsupported public key: %v", err).WithCause(err)
	}
	name, ok := att.AAGUID.Name()
	if !ok {
		name = "Passkey"
	}
	userName := strings.TrimSpace(req.GetUserName())
	if len(userName) > maxNameLength {
		return nil, connect.Errorf(connect.CodeInvalidArgument, "name must be at most %d characters", maxNameLength)
	}
	if userName == "" {
		userName = "Trail runner"
	}
	user, err := s.repo.RegisterPasskey(ctx, &users.Registration{
		Challenge: challenge,
		UserName:  userName,
		Passkey: &users.Passkey{
			ID:             att.CredentialID,
			Name:           name,
			PublicKey:      publicKey,
			Algorithm:      int32(att.Algorithm),
			AAGUID:         att.AAGUID[:],
			Transports:     req.GetTransports(),
			BackupEligible: att.Flags.BackupEligible(),
			BackedUp:       att.Flags.BackedUp(),
			SignCount:      att.Counter,
		},
	})
	if err != nil {
		if pgErr := (*pgconn.PgError)(nil); errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return nil, connect.NewError(connect.CodeAlreadyExists, "this passkey is already registered")
		}
		return nil, s.toConnectError(ctx, err, "failed to register passkey")
	}
	resp := authv1.FinishPasskeyRegistrationResponse_builder{}.Build()
	if err := s.signIn(ctx, info.ResponseHeader(), user.ID()); err != nil {
		return nil, err
	}
	return resp, nil
}

func (s *Service) BeginPasskeyLogin(ctx context.Context, req *authv1.BeginPasskeyLoginRequest) (*authv1.BeginPasskeyLoginResponse, error) {
	challenge := &users.Challenge{Ceremony: users.CeremonyLogin, Challenge: randomBytes(32)}
	id, err := s.repo.CreateChallenge(ctx, challenge, s.nowFn().Add(challengeTTL))
	if err != nil {
		return nil, s.internalError(ctx, "failed to create challenge", err)
	}
	return authv1.BeginPasskeyLoginResponse_builder{
		ChallengeId: store.UUIDToString(id),
		Challenge:   challenge.Challenge,
		RpId:        s.rp.ID,
	}.Build(), nil
}

func (s *Service) FinishPasskeyLogin(ctx context.Context, req *authv1.FinishPasskeyLoginRequest) (*authv1.FinishPasskeyLoginResponse, error) {
	info, _ := connect.CallInfoForServerContext(ctx)
	challenge, err := s.takeChallenge(ctx, req.GetChallengeId(), users.CeremonyLogin)
	if err != nil {
		return nil, err
	}
	failed := connect.NewError(connect.CodeUnauthenticated, "the passkey could not be verified")
	passkey, err := s.repo.GetPasskey(ctx, req.GetCredentialId())
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, connect.NewError(connect.CodeUnauthenticated, "this passkey isn't registered")
		}
		return nil, s.internalError(ctx, "failed to get passkey", err)
	}
	if handle := req.GetUserHandle(); len(handle) > 0 {
		expected, _, err := s.userHandle(ctx, passkey.UserID)
		if err != nil {
			return nil, err
		}
		if subtle.ConstantTimeCompare(handle, expected) != 1 {
			return nil, failed
		}
	}
	publicKey, err := x509.ParsePKIXPublicKey(passkey.PublicKey)
	if err != nil {
		return nil, s.internalError(ctx, "failed to parse stored public key", err)
	}
	assertion, err := s.rp.VerifyAssertion(publicKey, webauthn.Algorithm(passkey.Algorithm), challenge.Challenge,
		req.GetClientDataJson(), req.GetAuthenticatorData(), req.GetSignature())
	if err != nil {
		s.log.DebugContext(ctx, "Invalid passkey assertion", slogor.Err(err))
		return nil, failed
	}
	if !assertion.Flags.UserPresent() || !assertion.Flags.UserVerified() {
		return nil, connect.NewError(connect.CodeUnauthenticated, "the passkey must verify the user, e.g. with a fingerprint, face or PIN")
	}
	// Authenticators that count signatures must always increase the count;
	// otherwise the passkey may have been cloned.
	if (assertion.Counter != 0 || passkey.SignCount != 0) && assertion.Counter <= passkey.SignCount {
		s.log.WarnContext(ctx, "Passkey signature counter did not increase", slog.String("user_id", store.UUIDToString(passkey.UserID)))
		return nil, failed
	}
	if err := s.repo.UsePasskey(ctx, passkey.ID, assertion.Counter, assertion.Flags.BackedUp()); err != nil {
		return nil, s.internalError(ctx, "failed to update passkey", err)
	}
	resp := authv1.FinishPasskeyLoginResponse_builder{}.Build()
	if err := s.signIn(ctx, info.ResponseHeader(), passkey.UserID); err != nil {
		return nil, err
	}
	return resp, nil
}

// isBootstrap returns whether the token is the admin bootstrap token and
// there are no administrators yet.
func (s *Service) isBootstrap(ctx context.Context, token string) (bool, error) {
	if s.bootstrapToken == "" || subtle.ConstantTimeCompare([]byte(token), []byte(s.bootstrapToken)) != 1 {
		return false, nil
	}
	hasAdmin, err := s.repo.HasAdmin(ctx)
	if err != nil {
		return false, err
	}
	return !hasAdmin, nil
}

// BootstrapHandler sends visitors of the home page to the invitation to
// create the first administrator, while there is none, if they are on the
// same machine as the server. This way, whichever browser opens a local
// server, e.g. the one of an editor, creates the administrator, and the
// bootstrap token isn't revealed to anyone else. Other requests are served
// by next.
func (s *Service) BootstrapHandler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.bootstrapToken != "" && r.Method == http.MethodGet && r.URL.Path == "/" && isLocal(r) {
			hasAdmin, err := s.repo.HasAdmin(r.Context())
			switch {
			case err != nil:
				s.report.Report(r.Context(), "Failed to check for administrators", err)
			case !hasAdmin:
				http.Redirect(w, r, html.TokenURL(InvitePath, s.bootstrapToken), http.StatusFound)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// isLocal returns whether a request comes from the same machine, for a
// local address.
func isLocal(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return false
	}
	if ip := net.ParseIP(host); ip == nil || !ip.IsLoopback() {
		return false
	}
	name := r.Host
	if h, _, err := net.SplitHostPort(r.Host); err == nil {
		name = h
	}
	if name == "localhost" || strings.HasSuffix(name, ".localhost") {
		return true
	}
	ip := net.ParseIP(name)
	return ip != nil && ip.IsLoopback()
}

// userHandle returns the WebAuthn user handle and name of an existing user.
// Users who didn't sign up with a passkey use their ID as their handle.
func (s *Service) userHandle(ctx context.Context, userID pgtype.UUID) ([]byte, string, error) {
	user, err := s.repo.GetPasskeyUser(ctx, userID)
	switch {
	case err == nil:
		return user.UserHandle, user.Name, nil
	case errors.Is(err, store.ErrNotFound):
		return userID.Bytes[:], "trailrunn.ing user", nil
	default:
		return nil, "", s.internalError(ctx, "failed to get user", err)
	}
}

func (s *Service) takeChallenge(ctx context.Context, id string, ceremony users.Ceremony) (*users.Challenge, error) {
	challengeID, err := store.StringToUUID(id)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, "invalid challenge ID")
	}
	challenge, err := s.repo.TakeChallenge(ctx, challengeID, ceremony)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, connect.NewError(connect.CodeFailedPrecondition, "the request expired, please try again")
		}
		return nil, s.internalError(ctx, "failed to get challenge", err)
	}
	return challenge, nil
}

// signIn creates a session for the user and sets the session cookie.
func (s *Service) signIn(ctx context.Context, header *connect.Header, userID pgtype.UUID) error {
	expiry := s.nowFn().Add(sessionTTL)
	sessionID, err := s.repo.CreateSession(ctx, userID, expiry)
	if err != nil {
		return s.internalError(ctx, "failed to create session", err)
	}
	header.Add("Set-Cookie", authn.SessionCookie(sessionID, expiry).String())
	return nil
}

func invitationToProto(inv *users.Invitation) *authv1.Invitation {
	b := authv1.Invitation_builder{
		Id:            store.UUIDToString(inv.ID),
		Kind:          authv1.InvitationKind_INVITATION_KIND_NEW_USER,
		Note:          inv.Note,
		CreatedByName: inv.CreatedByName,
		UserName:      inv.UserName,
		CreateTime:    timestamppb.New(inv.CreateTime),
		ExpireTime:    timestamppb.New(inv.ExpireTime),
	}
	if inv.Kind == users.InvitationAddPasskey {
		b.Kind = authv1.InvitationKind_INVITATION_KIND_ADD_PASSKEY
	}
	if !inv.UseTime.IsZero() {
		b.UseTime = timestamppb.New(inv.UseTime)
	}
	return b.Build()
}

func randomBytes(n int) []byte {
	b := make([]byte, n)
	// crypto/rand.Read never returns an error.
	_, _ = rand.Read(b)
	return b
}

func (s *Service) toConnectError(ctx context.Context, err error, msg string) error {
	switch {
	case errors.Is(err, users.ErrInvitationInvalid):
		return connect.NewError(connect.CodeFailedPrecondition, err.Error()).WithCause(err)
	case errors.Is(err, users.ErrLastPasskey):
		return connect.NewError(connect.CodeFailedPrecondition, err.Error()).WithCause(err)
	case errors.Is(err, store.ErrNotFound):
		return connect.NewError(connect.CodeNotFound, "not found")
	default:
		return s.internalError(ctx, msg, err)
	}
}

func (s *Service) internalError(ctx context.Context, msg string, err error) error {
	s.report.Report(ctx, msg, err)
	return connect.NewError(connect.CodeInternal, msg).WithCause(err)
}
