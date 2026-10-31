package quiz

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"sort"
	"testing"
)

// authenticator is a software WebAuthn authenticator producing real
// attestations and assertions, to test passkey ceremonies without a browser.
type authenticator struct {
	t            *testing.T
	origin, rpID string
	// Passkeys by credential ID.
	keys map[string]*softPasskey
	// Whether to report user verification.
	userVerified bool
}

type softPasskey struct {
	id         []byte
	key        *ecdsa.PrivateKey
	userHandle []byte
	counter    uint32
}

func newAuthenticator(t *testing.T, origin, rpID string) *authenticator {
	return &authenticator{t: t, origin: origin, rpID: rpID, keys: map[string]*softPasskey{}, userVerified: true}
}

const (
	flagUserPresent  = 0x01
	flagUserVerified = 0x04
	flagAttested     = 0x40
)

func (a *authenticator) flags() byte {
	f := byte(flagUserPresent)
	if a.userVerified {
		f |= flagUserVerified
	}
	return f
}

func (a *authenticator) clientData(typ string, challenge []byte) []byte {
	data, err := json.Marshal(map[string]string{
		"type":      typ,
		"challenge": base64.RawURLEncoding.EncodeToString(challenge),
		"origin":    a.origin,
	})
	if err != nil {
		a.t.Fatal(err)
	}
	return data
}

// create creates a passkey, like navigator.credentials.create(), returning
// the credential ID, client data JSON and attestation object.
func (a *authenticator) create(challenge, userHandle []byte) (credentialID, clientDataJSON, attestationObject []byte) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		a.t.Fatal(err)
	}
	id := make([]byte, 16)
	_, _ = rand.Read(id)
	a.keys[string(id)] = &softPasskey{id: id, key: key, userHandle: userHandle}

	rpIDHash := sha256.Sum256([]byte(a.rpID))
	var authData bytes.Buffer
	authData.Write(rpIDHash[:])
	authData.WriteByte(a.flags() | flagAttested)
	authData.Write([]byte{0, 0, 0, 0})
	authData.Write(make([]byte, 16)) // AAGUID
	_ = binary.Write(&authData, binary.BigEndian, uint16(len(id)))
	authData.Write(id)
	ecdh, err := key.PublicKey.ECDH()
	if err != nil {
		a.t.Fatal(err)
	}
	point := ecdh.Bytes() // 0x04 || X || Y
	// COSE_Key: kty EC2, alg ES256, crv P-256, x, y.
	authData.Write(cborMap(map[any]any{1: 2, 3: -7, -1: 1, -2: point[1:33], -3: point[33:]}))

	attestationObject = cborMap(map[any]any{
		"fmt":      "none",
		"attStmt":  map[any]any{},
		"authData": authData.Bytes(),
	})
	return id, a.clientData("webauthn.create", challenge), attestationObject
}

// get signs a challenge with a passkey, like navigator.credentials.get(),
// returning the client data JSON, authenticator data and signature.
func (a *authenticator) get(credentialID, challenge []byte) (clientDataJSON, authenticatorData, signature []byte) {
	p := a.keys[string(credentialID)]
	if p == nil {
		a.t.Fatalf("unknown credential")
	}
	p.counter++
	rpIDHash := sha256.Sum256([]byte(a.rpID))
	authData := append(rpIDHash[:], a.flags())
	authData = binary.BigEndian.AppendUint32(authData, p.counter)
	clientDataJSON = a.clientData("webauthn.get", challenge)
	clientDataHash := sha256.Sum256(clientDataJSON)
	digest := sha256.Sum256(append(append([]byte{}, authData...), clientDataHash[:]...))
	sig, err := ecdsa.SignASN1(rand.Reader, p.key, digest[:])
	if err != nil {
		a.t.Fatal(err)
	}
	return clientDataJSON, authData, sig
}

// cborMap encodes a map with integer or string keys and integer, string,
// byte string or map values as canonical CBOR.
func cborMap(m map[any]any) []byte {
	var buf bytes.Buffer
	cborEncode(&buf, m)
	return buf.Bytes()
}

func cborHeader(buf *bytes.Buffer, major byte, n uint64) {
	switch {
	case n < 24:
		buf.WriteByte(major<<5 | byte(n))
	case n < 1<<8:
		buf.WriteByte(major<<5 | 24)
		buf.WriteByte(byte(n))
	default:
		buf.WriteByte(major<<5 | 25)
		_ = binary.Write(buf, binary.BigEndian, uint16(n))
	}
}

func cborEncode(buf *bytes.Buffer, v any) {
	switch v := v.(type) {
	case int:
		if v >= 0 {
			cborHeader(buf, 0, uint64(v))
		} else {
			cborHeader(buf, 1, uint64(-1-v))
		}
	case string:
		cborHeader(buf, 3, uint64(len(v)))
		buf.WriteString(v)
	case []byte:
		cborHeader(buf, 2, uint64(len(v)))
		buf.Write(v)
	case map[any]any:
		// Canonical CBOR sorts keys by their encoded bytes.
		type entry struct{ key, value []byte }
		var entries []entry
		for k, val := range v {
			var kb, vb bytes.Buffer
			cborEncode(&kb, k)
			cborEncode(&vb, val)
			entries = append(entries, entry{kb.Bytes(), vb.Bytes()})
		}
		sort.Slice(entries, func(i, j int) bool {
			if len(entries[i].key) != len(entries[j].key) {
				return len(entries[i].key) < len(entries[j].key)
			}
			return bytes.Compare(entries[i].key, entries[j].key) < 0
		})
		cborHeader(buf, 5, uint64(len(entries)))
		for _, e := range entries {
			buf.Write(e.key)
			buf.Write(e.value)
		}
	default:
		panic("unsupported CBOR value")
	}
}
