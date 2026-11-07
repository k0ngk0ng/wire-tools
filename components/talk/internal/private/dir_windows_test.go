package private

import (
	"golang.org/x/sys/windows"
	"testing"
	"unsafe"
)

func TestPrivateDirectoryProtectedACL(t *testing.T) {
	dir := t.TempDir()
	if err := Dir(dir); err != nil {
		t.Fatal(err)
	}
	sd, err := windows.GetNamedSecurityInfo(dir, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	control, _, err := sd.Control()
	if err != nil {
		t.Fatal(err)
	}
	if control&windows.SE_DACL_PROTECTED == 0 {
		t.Fatalf("ACL inheritance is not protected: %s", sd.String())
	}
	acl, _, err := sd.DACL()
	if err != nil {
		t.Fatal(err)
	}
	if acl.AceCount != 2 {
		t.Fatalf("unexpected grants: %s", sd.String())
	}
	u, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	system, err := windows.CreateWellKnownSid(windows.WinLocalSystemSid)
	if err != nil {
		t.Fatal(err)
	}
	// Compare SIDs, not SDDL strings: Windows abbreviates the built-in local
	// administrator as LA on hosted CI even when the input was a numeric SID.
	foundUser, foundSystem := false, false
	for i := uint32(0); i < uint32(acl.AceCount); i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err = windows.GetAce(acl, i, &ace); err != nil {
			t.Fatal(err)
		}
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		if sid.Equals(u.User.Sid) {
			foundUser = true
		} else if sid.Equals(system) {
			foundSystem = true
		} else {
			t.Fatalf("unexpected principal: %s", sid.String())
		}
	}
	if !foundUser || !foundSystem {
		t.Fatalf("missing required grants: %s", sd.String())
	}
}
