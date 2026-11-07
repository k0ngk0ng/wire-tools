//go:build windows

package auth

import (
	"testing"

	"github.com/k0ngk0ng/wire-tools/components/download/internal/private"
	"golang.org/x/sys/windows"
)

func TestWindowsSessionRoundTripUsesACLAndRejectsBroadGrant(t *testing.T) {
	dir := t.TempDir()
	session := Session{Origin: "https://files.example.com", Cookies: []Cookie{{Name: "sid", Value: "private", Domain: "files.example.com", Path: "/", Secure: true}}}
	if err := Save(dir, session); err != nil {
		t.Fatal(err)
	}
	path := sessionPath(dir, session.Origin)
	if err := private.Check(path); err != nil {
		t.Fatal(err)
	}
	if loaded, err := Load(dir, session.Origin); err != nil || loaded == nil {
		t.Fatalf("saved private session unavailable: %v", err)
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	sd, err := windows.SecurityDescriptorFromString("D:P(A;;FA;;;" + user.User.Sid.String() + ")(A;;FA;;;SY)(A;;FR;;;WD)")
	if err != nil {
		t.Fatal(err)
	}
	acl, _, err := sd.DACL()
	if err != nil {
		t.Fatal(err)
	}
	if err = windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, acl, nil); err != nil {
		t.Fatal(err)
	}
	if _, err = Load(dir, session.Origin); err == nil {
		t.Fatal("session with Everyone read grant was accepted")
	}
	if err = private.Check(path); err == nil {
		t.Fatal("session load silently repaired its unsafe ACL")
	}
}
