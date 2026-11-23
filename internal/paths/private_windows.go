package paths

import (
	"fmt"
	"os"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

// MakePrivate makes dir a folder only the user can use: a protected access
// list with only the user's full control, which what's in it inherits.
func MakePrivate(dir string) error {
	me, err := currentUser()
	if err != nil {
		return err
	}
	return ownDir(dir, me, false)
}

// readRights are the rights that let someone read a file's contents.
const readRights = windows.FILE_READ_DATA | windows.GENERIC_READ | windows.GENERIC_ALL

// OthersCanRead says who besides the user path's access list lets read it,
// and how to fix it. SYSTEM and Administrators are left out: they can read
// any file on Windows anyway, as root can on Unix.
func OthersCanRead(path string) (who, fix string, err error) {
	if _, err := os.Stat(path); err != nil {
		return "", "", err
	}
	me, err := currentUser()
	if err != nil {
		return "", "", err
	}
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return "", "", err
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		return "", "", err
	}
	readers, err := grantedOtherThan(dacl, me, readRights, windows.WinLocalSystemSid, windows.WinBuiltinAdministratorsSid)
	if err != nil || len(readers) == 0 {
		return "", "", err
	}
	return strings.Join(readers, ", "), fmt.Sprintf(`run icacls "%s" /inheritance:r /grant:r "%%USERNAME%%:F"`, path), nil
}

// grantedOtherThan lists the accounts, besides me and the well-known ones
// trusted, that an allow entry in dacl gives any of rights. A nil DACL lets
// everyone in.
func grantedOtherThan(dacl *windows.ACL, me *windows.SID, rights windows.ACCESS_MASK, trusted ...windows.WELL_KNOWN_SID_TYPE) ([]string, error) {
	if dacl == nil {
		return []string{"Everyone (no access list)"}, nil
	}
	var who []string
	for i := range uint32(dacl.AceCount) {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, i, &ace); err != nil {
			return nil, err
		}
		if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE || ace.Mask&rights == 0 {
			continue
		}
		// The SID starts where SidStart is, inside the ACE.
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart)) // #nosec G103 -- the ACE's own SID
		if sid.Equals(me) || isWellKnown(sid, trusted) {
			continue
		}
		who = append(who, accountName(sid))
	}
	return who, nil
}

func isWellKnown(sid *windows.SID, kinds []windows.WELL_KNOWN_SID_TYPE) bool {
	for _, k := range kinds {
		if sid.IsWellKnown(k) {
			return true
		}
	}
	return false
}

// accountName is sid as DOMAIN\name, or the SID when it can't be looked up.
func accountName(sid *windows.SID) string {
	if account, domain, _, err := sid.LookupAccount(""); err == nil {
		if domain != "" {
			return domain + `\` + account
		}
		return account
	}
	return sid.String()
}
