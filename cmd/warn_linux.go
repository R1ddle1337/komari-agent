//go:build linux

package cmd

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

const (
	linuxMOTDPath          = "/etc/motd"
	legacyUpdateMOTDPath   = "/etc/update-motd.d/99-komari-agent-warning"
	legacyUpdateMOTDMarker = "# Komari Agent managed MOTD warning"
	motdWarningStart       = "[Komari] Remote control is enabled on this device"
)

var motdWarningEnd = "Uninstall Komari Agent: " + warningUninstallURL + "\n"

type motdFile struct {
	target   string
	mode     os.FileMode
	uid      int
	gid      int
	exists   bool
	original string
}

func startSecurityWarning(ctx context.Context) func() {
	if ctx.Err() != nil {
		return func() {}
	}
	// Linux agents no longer install or maintain login banners. Existing
	// installations only migrate a recognizable legacy block once.
	migrateLegacyMOTD(linuxMOTDPath, legacyUpdateMOTDPath)
	return func() {}
}

func migrateLegacyMOTD(motdPath, hookPath string) {
	removeLegacyUpdateMOTDWarning(hookPath)
	if err := removeInstalledMOTDWarning(motdPath); err != nil {
		log.Printf("[warn] could not remove legacy Komari MOTD notice: %v", err)
	}
}

func removeInstalledMOTDWarning(path string) error {
	original, err := readMOTD(path)
	if err != nil {
		return err
	}
	if !original.exists {
		return nil
	}
	content, found, err := removeMOTDWarning(original.original)
	if err != nil {
		return err
	}
	if !found {
		return nil
	}
	return writeMOTD(original, []byte(content))
}

func removeLegacyUpdateMOTDWarning(path string) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return
	}
	if err != nil {
		log.Printf("[warn] could not inspect legacy update-motd hook: %v", err)
		return
	}
	if !strings.HasPrefix(string(data), legacyUpdateMOTDMarker+"\n") {
		log.Printf("[warn] legacy update-motd path is not managed by Komari; leaving it in place")
		return
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		log.Printf("[warn] could not remove legacy update-motd hook: %v", err)
	}
}

func removeMOTDWarning(content string) (string, bool, error) {
	start := strings.Index(content, motdWarningStart)
	if start < 0 {
		return content, false, nil
	}
	if strings.Contains(content[start+len(motdWarningStart):], motdWarningStart) {
		return "", false, fmt.Errorf("refusing to modify MOTD with multiple Komari warnings")
	}
	relativeEnd := strings.Index(content[start:], motdWarningEnd)
	if relativeEnd < 0 {
		return "", false, fmt.Errorf("refusing to modify incomplete Komari warning in MOTD")
	}
	end := start + relativeEnd + len(motdWarningEnd)
	before := content[:start]
	after := strings.TrimPrefix(content[end:], "\n")
	return before + after, true, nil
}

func readMOTD(path string) (motdFile, error) {
	file := motdFile{target: path, mode: 0644}
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return file, nil
	}
	if err != nil {
		return motdFile{}, err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		file.target, err = filepath.EvalSymlinks(path)
		if err != nil {
			return motdFile{}, err
		}
		info, err = os.Stat(file.target)
		if err != nil {
			return motdFile{}, err
		}
	}
	if !info.Mode().IsRegular() {
		return motdFile{}, fmt.Errorf("refusing to modify non-regular MOTD %s", path)
	}
	data, err := os.ReadFile(file.target)
	if err != nil {
		return motdFile{}, err
	}
	file.mode = info.Mode().Perm()
	file.exists = true
	file.original = string(data)
	if stat, ok := info.Sys().(*syscall.Stat_t); ok {
		file.uid = int(stat.Uid)
		file.gid = int(stat.Gid)
	}
	return file, nil
}

func writeMOTD(file motdFile, data []byte) error {
	temp, err := os.CreateTemp(filepath.Dir(file.target), ".komari-motd-*")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	defer temp.Close()
	if err := temp.Chmod(file.mode); err != nil {
		return err
	}
	if file.exists && (file.uid != 0 || file.gid != 0) {
		if err := temp.Chown(file.uid, file.gid); err != nil {
			return err
		}
	}
	if _, err := temp.Write(data); err != nil {
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(tempPath, file.target)
}
