//go:build linux

package cmd

import (
	"os"
	"path/filepath"
	"testing"
)

const legacyNotice = "[Komari] Remote control is enabled on this device\npanel.example can execute commands and read or modify files on this device as root.\nIf you did not set this up, your device may have been accessed without authorization.\nStop Komari Agent immediately and check your device for signs of compromise.\n\nUninstall Komari Agent: https://komari-document.pages.dev/en/faq/uninstall\n"

func TestFreshAgentNeverCreatesOrChangesMOTD(t *testing.T) {
	dir := t.TempDir()
	path, hook := filepath.Join(dir, "motd"), filepath.Join(dir, "hook")
	migrateLegacyMOTD(path, hook)
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatalf("fresh install created MOTD: %v", err)
	}
	const original = "Administrator welcome.\n"
	if err := os.WriteFile(path, []byte(original), 0640); err != nil {
		t.Fatal(err)
	}
	before, _ := os.Stat(path)
	migrateLegacyMOTD(path, hook)
	after, _ := os.Stat(path)
	content, err := os.ReadFile(path)
	if err != nil || string(content) != original || !os.SameFile(before, after) || !before.ModTime().Equal(after.ModTime()) {
		t.Fatal("normal MOTD was rewritten")
	}
	if _, err := os.Lstat(hook); !os.IsNotExist(err) {
		t.Fatal("created an update-motd hook")
	}
}

func TestUpgradeRemovesOnlyLegacyNotice(t *testing.T) {
	for _, symlink := range []bool{false, true} {
		dir := t.TempDir()
		path := filepath.Join(dir, "motd")
		target := path
		if symlink {
			target = filepath.Join(dir, "real-motd")
		}
		const before = "Administrator banner\n\n"
		const after = "Keep this footer\n"
		if err := os.WriteFile(target, []byte(before+legacyNotice+after), 0640); err != nil {
			t.Fatal(err)
		}
		if symlink {
			if err := os.Symlink(target, path); err != nil {
				t.Fatal(err)
			}
		}
		migrateLegacyMOTD(path, filepath.Join(dir, "hook"))
		content, err := os.ReadFile(path)
		if err != nil || string(content) != before+after {
			t.Fatalf("lost administrator content: %q %v", content, err)
		}
		info, _ := os.Stat(path)
		if info.Mode().Perm() != 0640 {
			t.Fatal("changed permissions")
		}
		if symlink {
			l, _ := os.Lstat(path)
			if l.Mode()&os.ModeSymlink == 0 {
				t.Fatal("replaced symlink")
			}
		}
		migrateLegacyMOTD(path, filepath.Join(dir, "hook"))
		again, _ := os.Stat(path)
		if !os.SameFile(info, again) {
			t.Fatal("repeated startup modified clean MOTD")
		}
	}
}

func TestMalformedLegacyMOTDIsUntouched(t *testing.T) {
	for _, content := range []string{motdWarningStart + "\npartial\n", legacyNotice + legacyNotice} {
		path := filepath.Join(t.TempDir(), "motd")
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
		if err := removeInstalledMOTDWarning(path); err == nil {
			t.Fatal("accepted ambiguous notice")
		}
		got, _ := os.ReadFile(path)
		if string(got) != content {
			t.Fatal("modified ambiguous MOTD")
		}
	}
}

func TestOnlyManagedLegacyHookIsRemoved(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "motd")
	hook := filepath.Join(dir, "hook")
	for _, content := range []string{legacyUpdateMOTDMarker + "\nprintf warning\n", "#!/bin/sh\nprintf administrator\n"} {
		if err := os.WriteFile(hook, []byte(content), 0755); err != nil {
			t.Fatal(err)
		}
		migrateLegacyMOTD(path, hook)
		got, err := os.ReadFile(hook)
		if content[0] == '#' && content[:2] != "#!" {
			if !os.IsNotExist(err) {
				t.Fatal("managed hook remained")
			}
		} else if err != nil || string(got) != content {
			t.Fatal("administrator hook changed")
		}
	}
}
