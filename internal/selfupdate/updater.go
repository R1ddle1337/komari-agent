// Package selfupdate implements the owned agent's raw-binary release protocol.
// Archive extraction and PGP validation are deliberately absent: published
// assets are exact platform binaries with mandatory SHA-256 sidecars.
package selfupdate

import (
	"bytes"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/blang/semver"
	binaryupdate "github.com/inconshreveable/go-update"
	"github.com/komari-monitor/komari-agent/utils"
)

const maxBinaryBytes = 64 << 20
const maxMetadataBytes = 4 << 20

type Release struct {
	Version           semver.Version
	AssetURL          string
	AssetByteSize     int
	AssetID           int64
	ValidationAssetID int64
	URL               string
	ReleaseNotes      string
	Name              string
	PublishedAt       *time.Time
	RepoOwner         string
	RepoName          string
}

type Validator interface {
	Suffix() string
	Validate(binary, checksum []byte) error
}

type SHA2Validator struct{}

func (*SHA2Validator) Suffix() string { return ".sha256" }
func (*SHA2Validator) Validate(binary, checksum []byte) error {
	if len(checksum) < 64 {
		return errors.New("invalid SHA256 checksum file")
	}
	expected, err := hex.DecodeString(string(checksum[:64]))
	if err != nil {
		return errors.New("invalid SHA256 checksum file")
	}
	sum := sha256.Sum256(binary)
	if subtle.ConstantTimeCompare(sum[:], expected) != 1 {
		return errors.New("SHA256 checksum mismatch")
	}
	return nil
}

type Config struct {
	AssetName  string
	Validator  Validator
	HTTPClient *http.Client
	APIBaseURL string
	APIToken   string
}

type Updater struct{ config Config }

func NewUpdater(config Config) (*Updater, error) {
	if config.AssetName == "" || config.Validator == nil {
		return nil, errors.New("asset name and checksum validator are required")
	}
	if config.HTTPClient == nil {
		config.HTTPClient = &http.Client{Timeout: 60 * time.Second}
	}
	if config.APIBaseURL == "" {
		config.APIBaseURL = "https://api.github.com"
	}
	return &Updater{config: config}, nil
}

func (u *Updater) get(path, accept string, limit int64) ([]byte, error) {
	req, err := http.NewRequest(http.MethodGet, strings.TrimRight(u.config.APIBaseURL, "/")+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", accept)
	req.Header.Set("User-Agent", "komari-agent")
	if u.config.APIToken != "" {
		req.Header.Set("Authorization", "Bearer "+u.config.APIToken)
	}
	response, err := u.config.HTTPClient.Do(req)
	if err != nil {
		return nil, utils.SanitizeHTTPError(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub returned HTTP %d", response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > limit {
		return nil, fmt.Errorf("release response exceeds %d bytes", limit)
	}
	return body, nil
}

func repoPath(owner, repo string) string {
	return "/repos/" + url.PathEscape(owner) + "/" + url.PathEscape(repo)
}

func (u *Updater) DetectLatest(slug string) (*Release, bool, error) {
	parts := strings.Split(slug, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return nil, false, errors.New("invalid repository")
	}
	var latest *Release
	for page := 1; page <= 20; page++ {
		body, err := u.get(fmt.Sprintf("%s/releases?per_page=100&page=%d", repoPath(parts[0], parts[1]), page), "application/vnd.github+json", maxMetadataBytes)
		if err != nil {
			return nil, false, err
		}
		var releases []struct {
			Tag         string     `json:"tag_name"`
			Name        string     `json:"name"`
			Body        string     `json:"body"`
			URL         string     `json:"html_url"`
			Draft       bool       `json:"draft"`
			Prerelease  bool       `json:"prerelease"`
			PublishedAt *time.Time `json:"published_at"`
			Assets      []struct {
				ID   int64  `json:"id"`
				Name string `json:"name"`
				URL  string `json:"browser_download_url"`
				Size int    `json:"size"`
			} `json:"assets"`
		}
		if err = json.Unmarshal(body, &releases); err != nil {
			return nil, false, err
		}
		for _, item := range releases {
			if item.Draft || item.Prerelease {
				continue
			}
			version, err := semver.ParseTolerant(strings.TrimPrefix(strings.TrimPrefix(item.Tag, "v"), "V"))
			if err != nil || len(version.Pre) > 0 || (latest != nil && version.Compare(latest.Version) <= 0) {
				continue
			}
			candidate := &Release{Version: version, RepoOwner: parts[0], RepoName: parts[1], Name: item.Name, ReleaseNotes: item.Body, URL: item.URL, PublishedAt: item.PublishedAt}
			for _, asset := range item.Assets {
				if asset.Name == u.config.AssetName {
					candidate.AssetID = asset.ID
					candidate.AssetURL = asset.URL
					candidate.AssetByteSize = asset.Size
				}
				if asset.Name == u.config.AssetName+u.config.Validator.Suffix() {
					candidate.ValidationAssetID = asset.ID
				}
			}
			if candidate.AssetID > 0 {
				latest = candidate
			}
		}
		if len(releases) < 100 {
			if latest != nil && latest.ValidationAssetID <= 0 {
				return nil, false, errors.New("release is missing " + u.config.Validator.Suffix() + " checksum asset")
			}
			return latest, latest != nil, nil
		}
	}
	return nil, false, errors.New("release listing exceeds 20 pages")
}

func (u *Updater) UpdateTo(release *Release, target string) error {
	if release.AssetID <= 0 || release.ValidationAssetID <= 0 {
		return errors.New("release binary and checksum assets are required")
	}
	if release.AssetByteSize > maxBinaryBytes {
		return errors.New("release binary exceeds size limit")
	}
	prefix := repoPath(release.RepoOwner, release.RepoName) + "/releases/assets/"
	checksum, err := u.get(fmt.Sprintf("%s%d", prefix, release.ValidationAssetID), "application/octet-stream", 4096)
	if err != nil {
		return err
	}
	binary, err := u.get(fmt.Sprintf("%s%d", prefix, release.AssetID), "application/octet-stream", maxBinaryBytes)
	if err != nil {
		return err
	}
	if len(binary) == 0 {
		return errors.New("empty release binary")
	}
	if err = u.config.Validator.Validate(binary, checksum); err != nil {
		return fmt.Errorf("validating asset content: %w", err)
	}
	info, err := os.Stat(target)
	if err != nil {
		return err
	}
	// Retain the same cross-platform atomic replacement implementation used by
	// the previous updater, including its rollback if renaming fails.
	sum := sha256.Sum256(binary)
	return binaryupdate.Apply(bytes.NewReader(binary), binaryupdate.Options{TargetPath: target, TargetMode: info.Mode().Perm(), Checksum: sum[:]})
}
