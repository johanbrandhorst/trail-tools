package users

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/johanbrandhorst/trail-tools/internal/store"
	"github.com/johanbrandhorst/trail-tools/internal/users/internal/gen"
)

// ErrInvitationInvalid is returned when an invitation doesn't exist, has
// expired or has already been used.
var ErrInvitationInvalid = errors.New("the invitation is invalid, expired or already used")

// ErrLastPasskey is returned when deleting a user's only passkey.
var ErrLastPasskey = errors.New("can't delete the only passkey of the account")

// PasskeyUser is a user who signs in with passkeys.
type PasskeyUser struct {
	id pgtype.UUID
	// The name of the user.
	Name string
	// The WebAuthn user handle, identifying the user to authenticators.
	UserHandle []byte
	// The key of the user's avatar image in the blob store. Empty if the
	// user has no avatar.
	AvatarKey string
	admin     bool
}

// ID returns the user's ID.
func (u *PasskeyUser) ID() pgtype.UUID {
	return u.id
}

// IsAdmin returns whether the user is an administrator.
func (u *PasskeyUser) IsAdmin() bool {
	return u.admin
}

// Passkey is a passkey credential of a user.
type Passkey struct {
	// The credential ID.
	ID     []byte
	UserID pgtype.UUID
	// The authenticator name, e.g. "iCloud Keychain".
	Name string
	// The PKIX encoded public key.
	PublicKey []byte
	// The COSE algorithm identifier.
	Algorithm      int32
	AAGUID         []byte
	Transports     []string
	BackupEligible bool
	BackedUp       bool
	SignCount      uint32
	CreateTime     time.Time
	// Zero if the passkey hasn't been used to sign in.
	LastUsedTime time.Time
}

// Ceremony is a WebAuthn ceremony.
type Ceremony string

// WebAuthn ceremonies.
const (
	CeremonyRegistration Ceremony = "registration"
	CeremonyLogin        Ceremony = "login"
)

// Challenge is a challenge issued to a browser for a WebAuthn ceremony.
type Challenge struct {
	ID        pgtype.UUID
	Ceremony  Ceremony
	Challenge []byte
	// For registrations, the user handle of the new credential.
	UserHandle []byte
	// For registrations, what authorizes the new passkey: an invitation,
	// a signed in user adding a passkey, or the admin bootstrap.
	InvitationID pgtype.UUID
	UserID       pgtype.UUID
	Bootstrap    bool
}

// InvitationKind is what an invitation allows.
type InvitationKind string

// Invitation kinds.
const (
	// The invitation creates a new user.
	InvitationNewUser InvitationKind = "new_user"
	// The invitation adds a passkey to an existing user.
	InvitationAddPasskey InvitationKind = "add_passkey"
)

// Invitation allows registering a passkey.
type Invitation struct {
	ID            pgtype.UUID
	Kind          InvitationKind
	CreatedBy     pgtype.UUID
	CreatedByName string
	// The user to add a passkey to, for add passkey invitations.
	UserID     pgtype.UUID
	UserName   string
	Note       string
	CreateTime time.Time
	ExpireTime time.Time
	// Zero if the invitation hasn't been used.
	UseTime time.Time
}

// HasAdmin returns whether any user is an administrator.
func (r *Repository) HasAdmin(ctx context.Context) (bool, error) {
	has, err := r.querier.HasAdmin(ctx)
	if err != nil {
		return false, fmt.Errorf("failed to check for admins: %w", err)
	}
	return has, nil
}

// GetPasskeyUser gets a passkey user by ID. It returns store.ErrNotFound if
// the user doesn't exist or isn't a passkey user.
func (r *Repository) GetPasskeyUser(ctx context.Context, userID pgtype.UUID) (*PasskeyUser, error) {
	row, err := r.querier.GetPasskeyUser(ctx, userID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, store.ErrNotFound
		}
		return nil, fmt.Errorf("failed to get passkey user: %w", err)
	}
	return &PasskeyUser{id: row.ID, Name: row.Name, UserHandle: row.UserHandle, AvatarKey: row.AvatarKey.String, admin: row.IsAdmin}, nil
}

// CreateChallenge stores a challenge for a WebAuthn ceremony until expiry,
// and returns its ID.
func (r *Repository) CreateChallenge(ctx context.Context, c *Challenge, expiry time.Time) (pgtype.UUID, error) {
	// Clean up old challenges while we're here.
	if err := r.querier.DeleteExpiredPasskeyChallenges(ctx); err != nil {
		return pgtype.UUID{}, fmt.Errorf("failed to delete expired challenges: %w", err)
	}
	id, err := r.querier.CreatePasskeyChallenge(ctx, &gen.CreatePasskeyChallengeParams{
		Ceremony:     gen.PasskeyCeremony(c.Ceremony),
		Challenge:    c.Challenge,
		UserHandle:   c.UserHandle,
		InvitationID: c.InvitationID,
		UserID:       c.UserID,
		Bootstrap:    c.Bootstrap,
		ExpireTime:   pgtype.Timestamptz{Time: expiry, Valid: true},
	})
	if err != nil {
		return pgtype.UUID{}, fmt.Errorf("failed to create challenge: %w", err)
	}
	return id, nil
}

// TakeChallenge returns and deletes a challenge, so it can only be used
// once. It returns store.ErrNotFound if the challenge doesn't exist or has
// expired.
func (r *Repository) TakeChallenge(ctx context.Context, id pgtype.UUID, ceremony Ceremony) (*Challenge, error) {
	c, err := r.querier.TakePasskeyChallenge(ctx, &gen.TakePasskeyChallengeParams{ID: id, Ceremony: gen.PasskeyCeremony(ceremony)})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, store.ErrNotFound
		}
		return nil, fmt.Errorf("failed to get challenge: %w", err)
	}
	return &Challenge{
		ID:           c.ID,
		Ceremony:     Ceremony(c.Ceremony),
		Challenge:    c.Challenge,
		UserHandle:   c.UserHandle,
		InvitationID: c.InvitationID,
		UserID:       c.UserID,
		Bootstrap:    c.Bootstrap,
	}, nil
}

// CreateInvitation creates an invitation and returns it with its secret
// token. Only a hash of the token is stored.
func (r *Repository) CreateInvitation(ctx context.Context, createdBy pgtype.UUID, kind InvitationKind, userID pgtype.UUID, note string, expiry time.Time) (*Invitation, string, error) {
	token := rand.Text()
	hash := sha256.Sum256([]byte(token))
	inv, err := r.querier.CreateInvitation(ctx, &gen.CreateInvitationParams{
		TokenSHA256: hash[:],
		Kind:        gen.InvitationKind(kind),
		CreatedBy:   createdBy,
		UserID:      userID,
		Note:        note,
		ExpireTime:  pgtype.Timestamptz{Time: expiry, Valid: true},
	})
	if err != nil {
		return nil, "", fmt.Errorf("failed to create invitation: %w", err)
	}
	return &Invitation{
		ID:         inv.ID,
		Kind:       InvitationKind(inv.Kind),
		CreatedBy:  inv.CreatedBy,
		UserID:     inv.UserID,
		Note:       inv.Note,
		CreateTime: inv.CreateTime.Time,
		ExpireTime: inv.ExpireTime.Time,
	}, token, nil
}

// GetInvitationByToken gets an unused, unexpired invitation by its token. It
// returns ErrInvitationInvalid otherwise.
func (r *Repository) GetInvitationByToken(ctx context.Context, token string) (*Invitation, error) {
	hash := sha256.Sum256([]byte(token))
	row, err := r.querier.GetInvitationByToken(ctx, hash[:])
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrInvitationInvalid
		}
		return nil, fmt.Errorf("failed to get invitation: %w", err)
	}
	inv := invitationFromDB(row.ID, row.Kind, row.CreatedBy, row.CreatedByName, row.UserID, row.UserName, row.Note, row.CreateTime, row.ExpireTime, row.UseTime)
	if !inv.UseTime.IsZero() || time.Now().After(inv.ExpireTime) {
		return nil, ErrInvitationInvalid
	}
	return inv, nil
}

// ListInvitations lists the invitations created by a user, newest first.
func (r *Repository) ListInvitations(ctx context.Context, createdBy pgtype.UUID) ([]*Invitation, error) {
	rows, err := r.querier.ListInvitations(ctx, createdBy)
	if err != nil {
		return nil, fmt.Errorf("failed to list invitations: %w", err)
	}
	invitations := make([]*Invitation, len(rows))
	for i, row := range rows {
		invitations[i] = invitationFromDB(row.ID, row.Kind, row.CreatedBy, row.CreatedByName, row.UserID, row.UserName, row.Note, row.CreateTime, row.ExpireTime, row.UseTime)
	}
	return invitations, nil
}

// DeleteInvitation deletes an invitation created by the user.
func (r *Repository) DeleteInvitation(ctx context.Context, createdBy, id pgtype.UUID) error {
	n, err := r.querier.DeleteInvitation(ctx, &gen.DeleteInvitationParams{ID: id, CreatedBy: createdBy})
	if err != nil {
		return fmt.Errorf("failed to delete invitation: %w", err)
	}
	if n == 0 {
		return store.ErrNotFound
	}
	return nil
}

// ListPasskeys lists a user's passkeys, oldest first.
func (r *Repository) ListPasskeys(ctx context.Context, userID pgtype.UUID) ([]*Passkey, error) {
	rows, err := r.querier.ListPasskeys(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to list passkeys: %w", err)
	}
	passkeys := make([]*Passkey, len(rows))
	for i, row := range rows {
		passkeys[i] = passkeyFromDB(row)
	}
	return passkeys, nil
}

// ListPasskeyIDs lists the credential IDs of a user's passkeys.
func (r *Repository) ListPasskeyIDs(ctx context.Context, userID pgtype.UUID) ([][]byte, error) {
	ids, err := r.querier.ListPasskeyIDs(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to list passkeys: %w", err)
	}
	return ids, nil
}

// GetPasskey gets a passkey by credential ID.
func (r *Repository) GetPasskey(ctx context.Context, credentialID []byte) (*Passkey, error) {
	row, err := r.querier.GetPasskey(ctx, credentialID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, store.ErrNotFound
		}
		return nil, fmt.Errorf("failed to get passkey: %w", err)
	}
	return passkeyFromDB(row), nil
}

// UsePasskey records a sign in with a passkey.
func (r *Repository) UsePasskey(ctx context.Context, credentialID []byte, signCount uint32, backedUp bool) error {
	if err := r.querier.UsePasskey(ctx, &gen.UsePasskeyParams{ID: credentialID, SignCount: int64(signCount), BackedUp: backedUp}); err != nil {
		return fmt.Errorf("failed to update passkey: %w", err)
	}
	return nil
}

// DeletePasskey deletes one of a user's passkeys, and ends the sessions it
// started, other than the session with keepToken, which the user deletes it
// from. Sessions started before sessions recorded their passkey end too, as
// any passkey may have started them. Passkey users can't delete their last
// passkey, as they would be locked out.
func (r *Repository) DeletePasskey(ctx context.Context, userID pgtype.UUID, credentialID []byte, keepToken string) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("failed to start transaction: %w", err)
	}
	defer tx.Rollback(context.Background())
	q := r.querier.WithTx(tx)
	count, err := q.CountPasskeys(ctx, userID)
	if err != nil {
		return fmt.Errorf("failed to count passkeys: %w", err)
	}
	if _, err := q.GetPasskeyUser(ctx, userID); err == nil && count <= 1 {
		return ErrLastPasskey
	}
	keepHash := sha256.Sum256([]byte(keepToken))
	if err := q.DeletePasskeySessions(ctx, &gen.DeletePasskeySessionsParams{
		UserID: userID, PasskeyID: credentialID, KeepTokenSHA256: keepHash[:],
	}); err != nil {
		return fmt.Errorf("failed to end the passkey's sessions: %w", err)
	}
	n, err := q.DeletePasskey(ctx, &gen.DeletePasskeyParams{ID: credentialID, UserID: userID})
	if err != nil {
		return fmt.Errorf("failed to delete passkey: %w", err)
	}
	if n == 0 {
		return store.ErrNotFound
	}
	return tx.Commit(ctx)
}

// Registration is a verified passkey to register, and what authorizes it.
type Registration struct {
	// The challenge the passkey was created for.
	Challenge *Challenge
	// The name of the user, if a new user is created.
	UserName string
	// The passkey to store. Its UserID is set by RegisterPasskey.
	Passkey *Passkey
}

// RegisterPasskey stores a passkey. Depending on the challenge, it uses an
// invitation to create a new user or add the passkey to an existing user,
// adds it to the signed in user, or creates the first administrator. It
// returns the user the passkey belongs to.
func (r *Repository) RegisterPasskey(ctx context.Context, reg *Registration) (User, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to start transaction: %w", err)
	}
	defer tx.Rollback(context.Background())
	q := r.querier.WithTx(tx)
	c := reg.Challenge
	var userID pgtype.UUID
	createUser := func(admin bool) error {
		user, err := q.CreatePasskeyUser(ctx, &gen.CreatePasskeyUserParams{UserHandle: c.UserHandle, Name: reg.UserName})
		if err != nil {
			return fmt.Errorf("failed to create user: %w", err)
		}
		userID = user.ID
		if admin {
			if err := q.SetAdmin(ctx, &gen.SetAdminParams{ID: userID, IsAdmin: true}); err != nil {
				return fmt.Errorf("failed to make user an admin: %w", err)
			}
		}
		return nil
	}
	switch {
	case c.InvitationID.Valid:
		n, err := q.UseInvitation(ctx, c.InvitationID)
		if err != nil {
			return nil, fmt.Errorf("failed to use invitation: %w", err)
		}
		if n == 0 {
			return nil, ErrInvitationInvalid
		}
		inv, err := q.GetInvitation(ctx, c.InvitationID)
		if err != nil {
			return nil, fmt.Errorf("failed to get invitation: %w", err)
		}
		if InvitationKind(inv.Kind) == InvitationAddPasskey {
			userID = inv.UserID
		} else if err := createUser(false); err != nil {
			return nil, err
		}
	case c.Bootstrap:
		hasAdmin, err := q.HasAdmin(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to check for admins: %w", err)
		}
		if hasAdmin {
			return nil, ErrInvitationInvalid
		}
		if err := createUser(true); err != nil {
			return nil, err
		}
	case c.UserID.Valid:
		userID = c.UserID
	default:
		return nil, errors.New("the challenge doesn't authorize a registration")
	}
	p := reg.Passkey
	if _, err := q.CreatePasskey(ctx, &gen.CreatePasskeyParams{
		ID:             p.ID,
		UserID:         userID,
		Name:           p.Name,
		PublicKey:      p.PublicKey,
		Algorithm:      p.Algorithm,
		Aaguid:         p.AAGUID,
		Transports:     nonNilStrings(p.Transports),
		BackupEligible: p.BackupEligible,
		BackedUp:       p.BackedUp,
		SignCount:      int64(p.SignCount),
	}); err != nil {
		return nil, fmt.Errorf("failed to store passkey: %w", err)
	}
	user, err := r.loadUser(ctx, q, userID)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("failed to commit transaction: %w", err)
	}
	return user, nil
}

// GetUser loads a user by ID.
func (r *Repository) GetUser(ctx context.Context, userID pgtype.UUID) (User, error) {
	return r.loadUser(ctx, r.querier, userID)
}

// CreateSession creates a session for the user, signed in with the passkey
// with the credential ID, and returns its secret token, for the session
// cookie. Only a hash of the token is stored.
func (r *Repository) CreateSession(ctx context.Context, userID pgtype.UUID, passkeyID []byte, expiry time.Time) (string, error) {
	token := rand.Text()
	hash := sha256.Sum256([]byte(token))
	err := r.querier.CreateSession(ctx, &gen.CreateSessionParams{
		UserID:      userID,
		TokenSHA256: hash[:],
		Expiry:      pgtype.Timestamptz{Time: expiry, Valid: true},
		PasskeyID:   passkeyID,
	})
	if err != nil {
		return "", fmt.Errorf("failed to create session: %w", err)
	}
	return token, nil
}

func invitationFromDB(
	id pgtype.UUID,
	kind gen.InvitationKind,
	createdBy pgtype.UUID,
	createdByName string,
	userID pgtype.UUID,
	userName, note string,
	createTime, expireTime, useTime pgtype.Timestamptz,
) *Invitation {
	return &Invitation{
		ID:            id,
		Kind:          InvitationKind(kind),
		CreatedBy:     createdBy,
		CreatedByName: createdByName,
		UserID:        userID,
		UserName:      userName,
		Note:          note,
		CreateTime:    createTime.Time,
		ExpireTime:    expireTime.Time,
		UseTime:       useTime.Time,
	}
}

func passkeyFromDB(p *gen.Passkey) *Passkey {
	return &Passkey{
		ID:             p.ID,
		UserID:         p.UserID,
		Name:           p.Name,
		PublicKey:      p.PublicKey,
		Algorithm:      p.Algorithm,
		AAGUID:         p.Aaguid,
		Transports:     p.Transports,
		BackupEligible: p.BackupEligible,
		BackedUp:       p.BackedUp,
		SignCount:      uint32(p.SignCount),
		CreateTime:     p.CreateTime.Time,
		LastUsedTime:   p.LastUsedTime.Time,
	}
}

func nonNilStrings(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
