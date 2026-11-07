// Package pairing exchanges an existing room key using a short-lived numeric
// password. Only the inviter listens; there is no directory or relay service.
package pairing

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/elliptic"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"sync/atomic"
	"time"

	"github.com/schollz/pake/v3"
)

const (
	Lifetime    = 5 * time.Minute
	MaxAttempts = 5
	maxMessage  = 4096
	protocol    = "wire-talk/pair/v1"
)

// Invitation is single-use, including when delivery fails after authentication.
// Code is for display to the local user only; it is never sent over the network.
type Invitation struct {
	Code           string
	Expires        time.Time
	listener       net.Listener
	key            []byte
	attemptTimeout time.Duration
	started        atomic.Bool
}

func New(listen string, key []byte) (*Invitation, error) {
	return newInvitation(listen, key, Lifetime, 10*time.Second)
}

// Room invitations retain the original 32-byte payload. The authenticated mode
// marker makes direct invitations fail closed with older clients.
func NewWithMode(listen string, key []byte, mode string) (*Invitation, error) {
	if mode != "" && mode != "room" && mode != "direct" {
		return nil, errors.New("mode must be room or direct")
	}
	i, err := New(listen, key)
	if err == nil && mode == "direct" {
		i.key = append(i.key, 1)
	}
	return i, err
}

func newInvitation(listen string, key []byte, ttl, attemptTimeout time.Duration) (*Invitation, error) {
	if len(key) != 32 {
		return nil, errors.New("room key must be 32 bytes")
	}
	n, err := rand.Int(rand.Reader, big.NewInt(1000000))
	if err != nil {
		return nil, err
	}
	l, err := net.Listen("tcp", listen)
	if err != nil {
		return nil, fmt.Errorf("listen for pairing: %w", err)
	}
	return &Invitation{Code: fmt.Sprintf("%06d", n), Expires: time.Now().Add(ttl), listener: l, key: bytes.Clone(key), attemptTimeout: attemptTimeout}, nil
}

func (i *Invitation) Address() string { return i.listener.Addr().String() }
func (i *Invitation) Close() error    { return i.listener.Close() }

// Serve stops after one authenticated exchange, expiry, cancellation, or five
// failed connections in total (not per IP, so changing IP cannot bypass it).
func (i *Invitation) Serve(ctx context.Context) error {
	if !i.started.CompareAndSwap(false, true) {
		return errors.New("invitation already used")
	}
	ctx, cancel := context.WithDeadline(ctx, i.Expires)
	defer cancel()
	defer i.Close()
	stop := context.AfterFunc(ctx, func() { i.Close() })
	defer stop()
	for attempt := 0; attempt < MaxAttempts; attempt++ {
		conn, err := i.listener.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return err
		}
		used, err := i.exchange(ctx, conn)
		if used {
			return err
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
	}
	return errors.New("invitation closed after 5 failed attempts; generate a new code")
}

func (i *Invitation) exchange(ctx context.Context, conn net.Conn) (used bool, err error) {
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	deadline := time.Now().Add(i.attemptTimeout)
	if end, ok := ctx.Deadline(); ok && end.Before(deadline) {
		deadline = end
	}
	conn.SetDeadline(deadline)
	p, err := newPAKE(i.Code, 1)
	if err != nil {
		return false, err
	}
	b, err := readMessage(conn)
	if err != nil {
		return false, err
	}
	if err = updatePAKE(p, b, 0); err != nil {
		return false, err
	}
	if err = writeMessage(conn, publicPAKE(p)); err != nil {
		return false, err
	}
	key, err := p.SessionKey()
	if err != nil {
		return false, err
	}
	if err = writeMessage(conn, derive(key, "host-confirm")); err != nil {
		return false, err
	}
	proof, err := readMessage(conn)
	if err != nil {
		return false, err
	}
	if !hmac.Equal(proof, derive(key, "guest-confirm")) {
		return false, errors.New("pairing authentication failed")
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	// Authentication consumes the invitation before any room secret is sent.
	aead, err := keyCipher(key)
	if err != nil {
		return true, err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err = rand.Read(nonce); err != nil {
		return true, err
	}
	payload := aead.Seal(nonce, nonce, i.key, []byte(protocol))
	return true, writeMessage(conn, payload)
}

// Receive obtains the room key but never writes local configuration or opens
// audio devices. The caller saves it exclusively before starting a session.
func Receive(ctx context.Context, address, code string) ([]byte, error) {
	key, mode, err := ReceiveWithMode(ctx, address, code)
	if err == nil && mode == "direct" {
		return nil, errors.New("direct invitation requires a mode-aware client")
	}
	return key, err
}

func ReceiveWithMode(ctx context.Context, address, code string) ([]byte, string, error) {
	payload, err := receivePayload(ctx, address, code)
	if err != nil {
		return nil, "", err
	}
	if len(payload) == 32 {
		return payload, "", nil
	}
	if len(payload) == 33 && payload[32] == 1 {
		return payload[:32], "direct", nil
	}
	return nil, "", errors.New("invalid pairing mode")
}

func receivePayload(ctx context.Context, address, code string) ([]byte, error) {
	if !validCode(code) {
		return nil, errors.New("pairing code must contain exactly 6 digits")
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", address)
	if err != nil {
		return nil, fmt.Errorf("connect to invitation (TCP): %w", err)
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	deadline, _ := ctx.Deadline()
	conn.SetDeadline(deadline)
	p, err := newPAKE(code, 0)
	if err != nil {
		return nil, err
	}
	if err = writeMessage(conn, publicPAKE(p)); err != nil {
		return nil, err
	}
	b, err := readMessage(conn)
	if err != nil {
		return nil, err
	}
	if err = updatePAKE(p, b, 1); err != nil {
		return nil, err
	}
	key, err := p.SessionKey()
	if err != nil {
		return nil, err
	}
	proof, err := readMessage(conn)
	if err != nil {
		return nil, err
	}
	if !hmac.Equal(proof, derive(key, "host-confirm")) {
		return nil, errors.New("pairing authentication failed; check the code")
	}
	if err = writeMessage(conn, derive(key, "guest-confirm")); err != nil {
		return nil, err
	}
	payload, err := readMessage(conn)
	if err != nil {
		return nil, err
	}
	aead, err := keyCipher(key)
	if err != nil {
		return nil, err
	}
	if n := len(payload) - aead.NonceSize() - aead.Overhead(); n != 32 && n != 33 {
		return nil, errors.New("invalid pairing payload")
	}
	return aead.Open(nil, payload[:aead.NonceSize()], payload[aead.NonceSize():], []byte(protocol))
}

func validCode(code string) bool {
	if len(code) != 6 {
		return false
	}
	for _, c := range code {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

func newPAKE(code string, role int) (*pake.Pake, error) {
	// Bind protocol and ordered roles into PAKE's transcript. Use the standard
	// P-256 implementation, and explicit mutual key confirmation below.
	pw := sha256.Sum256([]byte(protocol + "/password/" + code))
	return pake.InitCurveWithIdentities(pw[:], role, "p256", []byte(protocol+"/guest"), []byte(protocol+"/host"))
}

func updatePAKE(p *pake.Pake, data []byte, role int) error {
	// Validate untrusted JSON before passing it to the library: null, absent
	// coordinates and wrong roles must return errors rather than cause panics.
	var q *pake.Pake
	if err := json.Unmarshal(data, &q); err != nil {
		return errors.New("invalid pairing handshake")
	}
	if q == nil || q.Role != role {
		return errors.New("invalid pairing role")
	}
	x, y := q.Xᵤ, q.Xᵥ
	if role == 1 {
		x, y = q.Yᵤ, q.Yᵥ
	}
	if x == nil || y == nil || !elliptic.P256().IsOnCurve(x, y) {
		return errors.New("invalid pairing point")
	}
	return p.Update(data)
}

func publicPAKE(p *pake.Pake) []byte {
	// An explicit wire type keeps private fields out even if the dependency's
	// struct or serialization changes. Only masked public points are needed.
	b, _ := json.Marshal(struct {
		Role           int
		Xᵤ, Xᵥ, Yᵤ, Yᵥ *big.Int
	}{p.Role, p.Xᵤ, p.Xᵥ, p.Yᵤ, p.Yᵥ})
	return b
}

func derive(key []byte, purpose string) []byte {
	h := hmac.New(sha256.New, key)
	h.Write([]byte(protocol + "/" + purpose))
	return h.Sum(nil)
}

func keyCipher(key []byte) (cipher.AEAD, error) {
	b, err := aes.NewCipher(derive(key, "room-key-encryption"))
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(b)
}

func writeMessage(w io.Writer, b []byte) error {
	if len(b) == 0 || len(b) > maxMessage {
		return errors.New("invalid pairing message size")
	}
	data := make([]byte, 4+len(b))
	binary.BigEndian.PutUint32(data, uint32(len(b)))
	copy(data[4:], b)
	_, err := io.Copy(w, bytes.NewReader(data))
	return err
}

func readMessage(r io.Reader) ([]byte, error) {
	var header [4]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return nil, err
	}
	n := binary.BigEndian.Uint32(header[:])
	if n == 0 || n > maxMessage {
		return nil, errors.New("invalid pairing message size")
	}
	b := make([]byte, n)
	_, err := io.ReadFull(r, b)
	return b, err
}
