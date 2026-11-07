package pairing

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"testing"
	"time"
)

func invitation(t *testing.T, ttl time.Duration) (*Invitation, []byte, <-chan error) {
	t.Helper()
	key := make([]byte, 32)
	rand.Read(key)
	i, err := newInvitation("127.0.0.1:0", key, ttl, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- i.Serve(ctx) }()
	t.Cleanup(func() { cancel(); i.Close() })
	return i, key, done
}

func finished(t *testing.T, done <-chan error) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(3 * time.Second):
		t.Fatal("invitation did not stop")
		return nil
	}
}

func TestWrongCodeThenSuccessAndSingleUse(t *testing.T) {
	i, key, done := invitation(t, 5*time.Second)
	wrong := "000000"
	if i.Code == wrong {
		wrong = "000001"
	}
	if _, err := Receive(context.Background(), i.Address(), wrong); err == nil {
		t.Fatal("accepted wrong code")
	}
	got, err := Receive(context.Background(), i.Address(), i.Code)
	if err != nil || !bytes.Equal(got, key) {
		t.Fatalf("room exchange: %x %v", got, err)
	}
	if err := finished(t, done); err != nil {
		t.Fatal(err)
	}
	if _, err := Receive(context.Background(), i.Address(), i.Code); err == nil {
		t.Fatal("reused invitation")
	}
	if err := i.Serve(context.Background()); err == nil {
		t.Fatal("restarted invitation")
	}
}

func TestAttemptLimitIncludesMalformedConnections(t *testing.T) {
	i, _, done := invitation(t, 5*time.Second)
	for n := 0; n < MaxAttempts; n++ {
		conn, err := net.Dial("tcp", i.Address())
		if err != nil {
			t.Fatal(err)
		}
		conn.SetDeadline(time.Now().Add(time.Second))
		writeMessage(conn, []byte("null"))
		if _, err := readMessage(conn); err == nil {
			t.Fatal("accepted null handshake")
		}
		conn.Close()
	}
	if err := finished(t, done); err == nil {
		t.Fatal("did not report attempt exhaustion")
	}
	if _, err := Receive(context.Background(), i.Address(), i.Code); err == nil {
		t.Fatal("exhausted code still works")
	}
}

func TestExpiryAndStalledClient(t *testing.T) {
	i, _, done := invitation(t, 60*time.Millisecond)
	conn, err := net.Dial("tcp", i.Address())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := finished(t, done); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expiry: %v", err)
	}
	conn.SetReadDeadline(time.Now().Add(time.Second))
	if _, err := conn.Read(make([]byte, 1)); err == nil {
		t.Fatal("stalled connection remained open")
	}
}

func TestCancellationReleasesPort(t *testing.T) {
	i, err := New("127.0.0.1:0", make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	address := i.Address()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- i.Serve(ctx) }()
	conn, err := net.Dial("tcp", address)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	defer conn.Close()
	cancel()
	if err := finished(t, done); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	l, err := net.Listen("tcp", address)
	if err != nil {
		t.Fatal("port not released:", err)
	}
	l.Close()
}

func TestMutualConfirmationAndEncryptedPayload(t *testing.T) {
	// A real TCP proxy records the wire, or alters one framed message. Neither
	// a substituted key confirmation nor a tampered room key may be accepted.
	for _, corrupt := range []string{"none", "host-proof", "guest-proof", "payload"} {
		t.Run(corrupt, func(t *testing.T) {
			i, key, done := invitation(t, 5*time.Second)
			proxy, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer proxy.Close()
			recording := make(chan []byte, 1)
			go func() {
				client, err := proxy.Accept()
				if err != nil {
					recording <- nil
					return
				}
				defer client.Close()
				host, err := net.Dial("tcp", i.Address())
				if err != nil {
					recording <- nil
					return
				}
				defer host.Close()
				client.SetDeadline(time.Now().Add(time.Second))
				host.SetDeadline(time.Now().Add(time.Second))
				var wire bytes.Buffer
				transfer := func(dst, src net.Conn, label string) bool {
					b, err := readMessage(src)
					if err != nil {
						return false
					}
					wire.Write(b)
					if label == corrupt {
						b[len(b)-1] ^= 1
					}
					return writeMessage(dst, b) == nil
				}
				if transfer(host, client, "client-hello") && transfer(client, host, "host-hello") && transfer(client, host, "host-proof") && transfer(host, client, "guest-proof") {
					transfer(client, host, "payload")
				}
				recording <- wire.Bytes()
			}()
			got, err := Receive(context.Background(), proxy.Addr().String(), i.Code)
			if corrupt == "none" {
				if err != nil || !bytes.Equal(got, key) {
					t.Fatalf("exchange: %v", err)
				}
				if err := finished(t, done); err != nil {
					t.Fatal(err)
				}
			} else if err == nil {
				t.Fatal("accepted tampering")
			}
			wire := <-recording
			if len(wire) == 0 || bytes.Contains(wire, key) {
				t.Fatal("room key exposed or no transcript captured")
			}
			// Both wire directions carry only PAKE public values, confirmations,
			// and AEAD ciphertext. No password field is serialized.
			if bytes.Contains(wire, []byte(`"Pw"`)) {
				t.Fatal("password field serialized")
			}
		})
	}
}

func TestMessageBoundsAndCodeValidation(t *testing.T) {
	for _, n := range []uint32{0, maxMessage + 1, ^uint32(0)} {
		var b [4]byte
		binary.BigEndian.PutUint32(b[:], n)
		if _, err := readMessage(bytes.NewReader(b[:])); err == nil {
			t.Fatal("accepted size", n)
		}
	}
	if _, err := readMessage(bytes.NewReader([]byte{0, 0, 0, 1})); !errors.Is(err, io.EOF) {
		t.Fatal(err)
	}
	for _, s := range []string{"", "12345", "1234567", "12345a", "１２３４５６"} {
		if _, err := Receive(context.Background(), "unused", s); err == nil {
			t.Fatal("accepted", s)
		}
	}
	if !validCode("000123") {
		t.Fatal("lost leading zeros")
	}
}

func FuzzHandshake(f *testing.F) {
	for _, b := range []string{"null", "{}", `{"Role":0}`, `{"Role":0,"Xᵤ":0,"Xᵥ":0}`} {
		f.Add([]byte(b))
	}
	f.Fuzz(func(t *testing.T, b []byte) {
		if len(b) > maxMessage {
			return
		}
		p, err := newPAKE("123456", 1)
		if err != nil {
			t.Fatal(err)
		}
		_ = updatePAKE(p, b, 0)
	})
}
