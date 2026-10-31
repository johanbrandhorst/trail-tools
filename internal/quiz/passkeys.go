package quiz

import (
	"crypto/subtle"
	"crypto/x509"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-passkeys/go-passkeys/webauthn"
	"github.com/johanbrandhorst/trail-tools/internal/store"
)

const (
	ceremonyRegistration = "registration"
	ceremonyLogin        = "login"
)

// passkeyName is how a player's passkey is labeled in their passkey manager,
// so it isn't mistaken for one of the app's passkeys, which have the same
// relying party.
func passkeyName(name string) string {
	return name + " (LOTR quiz)"
}

type beginRegistrationResponse struct {
	ChallengeID string `json:"challengeId"`
	Challenge   []byte `json:"challenge"`
	RPID        string `json:"rpId"`
	UserHandle  []byte `json:"userHandle"`
	// UserName is the name to give the passkey.
	UserName string `json:"userName"`
}

func (s *Server) beginRegistration(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		Name string `json:"name"`
	}
	if err := readJSON(w, r, &req); err != nil {
		return err
	}
	name, err := cleanName(req.Name)
	if err != nil {
		return err
	}
	// Check the name before the player creates a passkey, which can't be
	// deleted from their device by the quiz if registering fails.
	taken, err := s.repo.nameTaken(r.Context(), name)
	if err != nil {
		return fmt.Errorf("failed to check name: %w", err)
	}
	if taken {
		return errNameTakenHTTP
	}
	c := &challenge{Ceremony: ceremonyRegistration, Challenge: randomBytes(32), UserHandle: randomBytes(32), UserName: name}
	id, err := s.repo.createChallenge(r.Context(), c, time.Now().Add(challengeTTL))
	if err != nil {
		return fmt.Errorf("failed to create challenge: %w", err)
	}
	writeJSON(w, http.StatusOK, beginRegistrationResponse{
		ChallengeID: store.UUIDToString(id),
		Challenge:   c.Challenge,
		RPID:        s.rp.ID,
		UserHandle:  c.UserHandle,
		UserName:    passkeyName(name),
	})
	return nil
}

// takeChallenge takes the challenge of a ceremony, which the browser has
// to complete in time.
func (s *Server) takeChallenge(r *http.Request, id, ceremony string) (*challenge, error) {
	uuid, err := store.StringToUUID(id)
	if err != nil {
		return nil, errorf(http.StatusBadRequest, "invalid challenge ID")
	}
	c, err := s.repo.takeChallenge(r.Context(), uuid, ceremony)
	if errors.Is(err, store.ErrNotFound) {
		return nil, errorf(http.StatusBadRequest, "the passkey prompt expired; try again")
	}
	if err != nil {
		return nil, fmt.Errorf("failed to take challenge: %w", err)
	}
	return c, nil
}

func (s *Server) finishRegistration(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		ChallengeID       string `json:"challengeId"`
		ClientDataJSON    []byte `json:"clientDataJson"`
		AttestationObject []byte `json:"attestationObject"`
	}
	if err := readJSON(w, r, &req); err != nil {
		return err
	}
	c, err := s.takeChallenge(r, req.ChallengeID, ceremonyRegistration)
	if err != nil {
		return err
	}
	att, err := s.rp.VerifyAttestation(c.Challenge, req.ClientDataJSON, req.AttestationObject)
	if err != nil {
		s.logDebug(r, "Invalid quiz passkey registration", err)
		return errorf(http.StatusBadRequest, "the passkey could not be verified; try again")
	}
	if !att.Flags.UserPresent() || !att.Flags.UserVerified() {
		return errorf(http.StatusBadRequest, "the passkey must verify you, e.g. with a fingerprint, face or PIN")
	}
	publicKey, err := x509.MarshalPKIXPublicKey(att.PublicKey)
	if err != nil {
		return errorf(http.StatusBadRequest, "this passkey's kind of key isn't supported; try another device")
	}
	u, err := s.repo.register(r.Context(), c.UserName, c.UserHandle, &passkey{
		ID:        att.CredentialID,
		PublicKey: publicKey,
		Algorithm: int32(att.Algorithm),
		SignCount: int64(att.Counter),
	})
	if errors.Is(err, errNameTaken) {
		return errNameTakenHTTP
	}
	if err != nil {
		return fmt.Errorf("failed to register: %w", err)
	}
	s.log.InfoContext(r.Context(), "Quiz player joined", slog.String("quiz_user_id", u.ID), slog.Bool("admin", u.IsAdmin))
	if err := s.signIn(w, r, u); err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, meResponse{User: &u})
	return nil
}

type beginLoginResponse struct {
	ChallengeID string `json:"challengeId"`
	Challenge   []byte `json:"challenge"`
	RPID        string `json:"rpId"`
}

func (s *Server) beginLogin(w http.ResponseWriter, r *http.Request) error {
	c := &challenge{Ceremony: ceremonyLogin, Challenge: randomBytes(32)}
	id, err := s.repo.createChallenge(r.Context(), c, time.Now().Add(challengeTTL))
	if err != nil {
		return fmt.Errorf("failed to create challenge: %w", err)
	}
	writeJSON(w, http.StatusOK, beginLoginResponse{ChallengeID: store.UUIDToString(id), Challenge: c.Challenge, RPID: s.rp.ID})
	return nil
}

func (s *Server) finishLogin(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		ChallengeID       string `json:"challengeId"`
		CredentialID      []byte `json:"credentialId"`
		ClientDataJSON    []byte `json:"clientDataJson"`
		AuthenticatorData []byte `json:"authenticatorData"`
		Signature         []byte `json:"signature"`
		UserHandle        []byte `json:"userHandle"`
	}
	if err := readJSON(w, r, &req); err != nil {
		return err
	}
	c, err := s.takeChallenge(r, req.ChallengeID, ceremonyLogin)
	if err != nil {
		return err
	}
	failed := errorf(http.StatusUnauthorized, "the passkey could not be verified; try again")
	pk, err := s.repo.getPasskey(r.Context(), req.CredentialID)
	if errors.Is(err, store.ErrNotFound) {
		// Browsers also offer the app's passkeys, which share the relying
		// party, and passkeys of players the quiz master removed.
		return errorf(http.StatusUnauthorized, "that passkey hasn't joined the quiz; enter your name to join with a new one")
	}
	if err != nil {
		return fmt.Errorf("failed to get passkey: %w", err)
	}
	if len(req.UserHandle) > 0 && subtle.ConstantTimeCompare(req.UserHandle, pk.UserHandle) != 1 {
		return failed
	}
	publicKey, err := x509.ParsePKIXPublicKey(pk.PublicKey)
	if err != nil {
		return fmt.Errorf("failed to parse stored public key: %w", err)
	}
	assertion, err := s.rp.VerifyAssertion(publicKey, webauthn.Algorithm(pk.Algorithm), c.Challenge,
		req.ClientDataJSON, req.AuthenticatorData, req.Signature)
	if err != nil {
		s.logDebug(r, "Invalid quiz passkey assertion", err)
		return failed
	}
	if !assertion.Flags.UserPresent() || !assertion.Flags.UserVerified() {
		return errorf(http.StatusUnauthorized, "the passkey must verify you, e.g. with a fingerprint, face or PIN")
	}
	// Authenticators that count signatures must always increase the count;
	// otherwise the passkey may have been cloned.
	if (assertion.Counter != 0 || pk.SignCount != 0) && int64(assertion.Counter) <= pk.SignCount {
		s.log.WarnContext(r.Context(), "Quiz passkey signature counter did not increase", slog.String("quiz_user_id", store.UUIDToString(pk.UserID)))
		return failed
	}
	if err := s.repo.usePasskey(r.Context(), pk.ID, assertion.Counter); err != nil {
		return fmt.Errorf("failed to update passkey: %w", err)
	}
	u, err := s.repo.getUser(r.Context(), store.UUIDToString(pk.UserID))
	if err != nil {
		return fmt.Errorf("failed to get user: %w", err)
	}
	if err := s.signIn(w, r, u); err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, meResponse{User: &u})
	return nil
}
