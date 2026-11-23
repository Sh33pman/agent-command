package paths

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/windows"
)

// On Windows a folder's access list stands in for Unix's 0700 and the socket's
// peer-uid check (internal/peer): AF_UNIX there has no peer credentials, so
// who may reach the daemon's socket is who may open its folder. Ensure gives
// the folder a protected access list (no ACEs inherited from above) with one
// entry, the user's full control, which the files and folders in it inherit.

// fileDeleteChild is FILE_DELETE_CHILD (winnt.h), which x/sys/windows
// doesn't name: removing what's in a folder.
const fileDeleteChild = 0x40

// writeRights are the rights that let someone change a folder or what's in
// it: add, replace or remove a socket, or change who may.
const writeRights = fileDeleteChild | windows.FILE_WRITE_DATA | windows.FILE_APPEND_DATA | windows.FILE_WRITE_EA |
	windows.FILE_WRITE_ATTRIBUTES | windows.DELETE |
	windows.WRITE_DAC | windows.WRITE_OWNER | windows.GENERIC_WRITE | windows.GENERIC_ALL

func ensureDir(dir string, realFolder bool) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	me, err := currentUser()
	if err != nil {
		return err
	}
	return ownDir(dir, me, realFolder)
}

// ownDir checks that dir is a folder, gives it an access list for only me, and
// checks no one else can write to it. A folder it can't take (another user's)
// is refused.
func ownDir(dir string, me *windows.SID, realFolder bool) error {
	if realFolder {
		lst, err := os.Lstat(dir)
		if err != nil {
			return err
		}
		// A junction (mount point) reads as irregular: it points elsewhere too.
		if lst.Mode()&(os.ModeSymlink|os.ModeIrregular) != 0 {
			return fmt.Errorf("%s is a symlink, not a folder: refusing to use it", dir)
		}
	}
	st, err := os.Stat(dir)
	if err != nil {
		return err
	}
	if !st.IsDir() {
		return fmt.Errorf("%s is not a folder", dir)
	}
	if ok, err := onlyFor(dir, me); err != nil {
		return err
	} else if ok {
		return nil
	}
	if err := restrict(dir, me); err != nil {
		return fmt.Errorf("%s: couldn't make it yours alone (%w): refusing to use it", dir, err)
	}
	if who, err := othersWhoCanWrite(dir, me); err != nil {
		return err
	} else if len(who) > 0 {
		return fmt.Errorf("%s can still be changed by %v: refusing to use it", dir, who)
	}
	return nil
}

// onlyFor says whether dir already belongs to me, with a protected access list
// that lets no one else write.
func onlyFor(dir string, me *windows.SID) (bool, error) {
	sd, err := windows.GetNamedSecurityInfo(dir, windows.SE_FILE_OBJECT,
		windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return false, err
	}
	owner, _, err := sd.Owner()
	if err != nil || owner == nil || !owner.Equals(me) {
		return false, err
	}
	control, _, err := sd.Control()
	if err != nil || control&windows.SE_DACL_PROTECTED == 0 {
		return false, err
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		return false, err
	}
	who, err := writersOtherThan(dacl, me)
	return err == nil && len(who) == 0, err
}

// restrict makes me dir's owner, and gives it a protected access list with
// only my full control, inherited by everything in it.
func restrict(dir string, me *windows.SID) error {
	acl, err := windows.ACLFromEntries([]windows.EXPLICIT_ACCESS{{
		AccessPermissions: windows.GENERIC_ALL,
		AccessMode:        windows.SET_ACCESS,
		Inheritance:       windows.SUB_CONTAINERS_AND_OBJECTS_INHERIT,
		Trustee: windows.TRUSTEE{
			TrusteeForm:  windows.TRUSTEE_IS_SID,
			TrusteeType:  windows.TRUSTEE_IS_USER,
			TrusteeValue: windows.TrusteeValueFromSID(me),
		},
	}}, nil)
	if err != nil {
		return err
	}
	return windows.SetNamedSecurityInfo(dir, windows.SE_FILE_OBJECT,
		windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		me, nil, acl, nil)
}

// othersWhoCanWrite lists who, besides me, dir's access list lets write.
func othersWhoCanWrite(dir string, me *windows.SID) ([]string, error) {
	sd, err := windows.GetNamedSecurityInfo(dir, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return nil, err
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		return nil, err
	}
	return writersOtherThan(dacl, me)
}

// writersOtherThan lists the accounts, besides me, that an allow entry in
// dacl gives a write right. A nil DACL lets everyone in.
func writersOtherThan(dacl *windows.ACL, me *windows.SID) ([]string, error) {
	return grantedOtherThan(dacl, me, writeRights)
}

// currentUser is the SID of the user this process runs as.
func currentUser() (*windows.SID, error) {
	u, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return nil, err
	}
	if u.User.Sid == nil {
		return nil, errors.New("this process's user has no SID")
	}
	return u.User.Sid.Copy()
}
