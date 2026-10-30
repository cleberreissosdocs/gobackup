// Package main (dirlist.go): implements the "list_dir" remote command,
// used by Central's folder-picker (the "Escolher pasta" button next to
// every origin/destination field). Windows has no single filesystem root
// like Linux's "/" — when asked for the root (the picker's starting
// point), this lists available drive letters instead of erroring out.
package winagentcore

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

type dirEntry struct {
	Name  string `json:"name"`
	IsDir bool   `json:"is_dir"`
}

// The browser's folder-picker JS is written Linux-style: it builds the
// next path as `currentPath + "/" + entryName` and goes up a level with
// `currentPath + "/.."`. Starting from an empty/root path (base == "" per
// the JS), clicking the "C:" drive entry sends "/C:", entering a
// subfolder from there sends "/C:/GoBackup", and going up from there
// sends "/C:/GoBackup/..". All of those have a bogus leading "/" in front
// of the drive letter that Windows has no concept of — leadingDriveSlash
// strips exactly that (and only that), wherever it appears at the very
// start of the path, regardless of what follows it.
var leadingDriveSlash = regexp.MustCompile(`^/([A-Za-z]:)`)

// bareDrive matches filepath.Clean's output for a path that reduces to
// just a drive letter with nothing after it — Go's Clean renders that as
// "C:." (NOT "C:"), because Windows treats bare "C:" as "current
// directory on drive C", distinct from that drive's actual root. We
// always mean the root here, so rewrite either spelling to "C:\".
var bareDrive = regexp.MustCompile(`^([A-Za-z]:)\.?$`)

// leadingUNCSlash strips the same bogus leading "/" the picker JS prepends
// (see leadingDriveSlash) when the path it is building is a UNC one
// (\\server\share) rather than a drive letter. Without this the path
// becomes \\\server\share, which is not recognizable as UNC -- and an
// unrecognized UNC path silently skips authentication and then fails as
// "path not found" instead of naming the real problem.
var leadingUNCSlash = regexp.MustCompile(`^[/\\]+(\\\\[^\\])`)

// NormalizeWindowsPath turns whatever shape the (Linux-authored) picker JS
// sent into a real Windows path os.ReadDir can use.
func NormalizeWindowsPath(path string) string {
	if path == "" {
		return ""
	}
	path = leadingDriveSlash.ReplaceAllString(path, "$1")
	// The JS always uses "/" as a separator; Windows paths themselves may
	// already contain "\" (typed by the user, or round-tripped from a
	// previous response) — normalize to "\" throughout, then let
	// filepath.Clean collapse any ".." segments (Go's filepath.Clean is
	// volume-aware on a Windows build: it never climbs the ".." above a
	// drive's own root, and always keeps the root separator for an
	// absolute path).
	path = strings.ReplaceAll(path, "/", `\`)
	// Re-apply the leading-slash fix AFTER separator normalization: the
	// picker builds "" + "/" + entry, so a UNC path arrives as
	// "/\\server\share" (or "\\\server\share" once slashes are
	// converted), neither of which is recognizable as UNC any more.
	path = leadingUNCSlash.ReplaceAllString(path, "$1")
	cleaned := filepath.Clean(path)
	if m := bareDrive.FindStringSubmatch(cleaned); m != nil {
		return m[1] + `\`
	}
	return cleaned
}

func listDir(payload map[string]any) (map[string]any, error) {
	path, _ := payload["path"].(string)

	// Browsing a network share has to work BEFORE the job is saved --
	// otherwise the operator would have to type the whole UNC path blind
	// to create the job that would let them browse it. So the picker may
	// pass credentials inline; they are used for this listing only and
	// never stored here (the job's own copy is what gets persisted,
	// encrypted, by create_job/update_job).
	if user, _ := payload["network_user"].(string); user != "" {
		pass, _ := payload["network_password"].(string)
		if err := ConnectUNC(NormalizeWindowsPath(path), user, pass); err != nil {
			return nil, err
		}
	}

	if path == "" || path == "/" || path == "\\" {
		drives := listDrives()
		if len(drives) > 0 {
			return map[string]any{"path": "", "entries": drives}, nil
		}
		// Fallback (e.g. running this build on Linux for local testing):
		// behave like a normal root listing.
		path = "/"
	} else {
		path = NormalizeWindowsPath(path)
	}

	entries, err := os.ReadDir(path)
	if err != nil {
		return nil, fmt.Errorf("list_dir %q: %w", path, err)
	}
	list := make([]dirEntry, 0, len(entries))
	for _, de := range entries {
		list = append(list, dirEntry{Name: de.Name(), IsDir: de.IsDir()})
	}
	return map[string]any{"path": path, "entries": list}, nil
}

// listDrives probes every possible drive letter (A:\ .. Z:\) and returns
// the ones that actually exist — the Windows equivalent of listing "/"'s
// children, since there's no single root directory to enumerate.
func listDrives() []dirEntry {
	var drives []dirEntry
	for c := 'A'; c <= 'Z'; c++ {
		root := string(c) + `:\`
		if info, err := os.Stat(root); err == nil && info.IsDir() {
			drives = append(drives, dirEntry{Name: string(c) + ":", IsDir: true})
		}
	}
	return drives
}
