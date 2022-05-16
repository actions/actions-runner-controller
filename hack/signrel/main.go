package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func main() {
	a := &githubReleaseAsset{}

	if len(os.Args) != 2 {
		fmt.Fprintf(os.Stderr, "Invalid command: %v\n", os.Args)
		fmt.Fprintf(os.Stderr, "USAGE: signrel [list-tags|sign-assets]\n")
		os.Exit(2)
	}

	switch cmd := os.Args[1]; cmd {
	case "tags":
		listTags(a)
	case "sign":
		tag := os.Getenv("TAG")
		sign(a, tag)
	default:
		fmt.Fprintf(os.Stderr, "Unknown command %s\n", cmd)
		os.Exit(2)
	}
}

func listTags(a *githubReleaseAsset) {
	_, err := a.getRecentReleases(owner, repo)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
}

func sign(a *githubReleaseAsset, tag string) {
	if err := a.Download(tag, "downloads"); err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
}

type Release struct {
	ID int64 `json:"id"`
}

type Asset struct {
	Name string `json:"name"`
	ID   int64  `json:"id"`
	URL  string `json:"url"`
}

type AssetsResponse struct {
	Assets []Asset
}

type githubReleaseAsset struct {
}

const (
	owner = "actions-runner-controller"
	repo  = "actions-runner-controller"
)

// GetFile downloads the give URL into the given path. The URL must
// reference a single file. If possible, the Getter should check if
// the remote end contains the same file and no-op this operation.
func (a *githubReleaseAsset) Download(tag string, dstDir string) error {
	release, err := a.getReleaseByTag(owner, repo, tag)
	if err != nil {
		return err
	}

	assets, err := a.getAssetsByReleaseID(owner, repo, release.ID)
	if err != nil {
		return err
	}

	d := filepath.Join(dstDir, tag)
	if err := os.MkdirAll(d, 0755); err != nil {
		return err
	}

	for _, asset := range assets {
		if strings.HasSuffix(asset.Name, ".asc") {
			continue
		}

		p := filepath.Join(d, asset.Name)
		fmt.Fprintf(os.Stderr, "Downloading %s to %s\n", asset.Name, p)
		if err := a.getFile(p, owner, repo, asset.ID); err != nil {
			return err
		}

		if info, _ := os.Stat(p + ".asc"); info == nil {
			_, err := a.sign(p)
			if err != nil {
				return err
			}
		}

		sig := p + ".asc"

		fmt.Fprintf(os.Stdout, "Uploading %s\n", sig)

		if err := a.upload(sig, release.ID); err != nil {
			return err
		}
	}

	return nil
}

func (a *githubReleaseAsset) getRecentReleases(owner, repo string) (*Release, error) {
	reqURL := fmt.Sprintf("https://api.github.com/repos/%s/%s/releases", owner, repo)

	req, err := http.NewRequest("GET", reqURL, nil)
	if err != nil {
		return nil, err
	}
	if gt := os.Getenv("GITHUB_TOKEN"); gt != "" {
		req.Header = make(http.Header)
		req.Header.Add("authorization", "token "+gt)
	}

	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: %s", reqURL, res.Status)
	}

	body, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, err
	}

	fmt.Fprintf(os.Stdout, "%s\n", string(body))

	return nil, nil
}

func (a *githubReleaseAsset) getReleaseByTag(owner, repo, tag string) (*Release, error) {
	var release Release

	reqURL := fmt.Sprintf("https://api.github.com/repos/%s/%s/releases/tags/%s", owner, repo, tag)

	req, err := http.NewRequest("GET", reqURL, nil)
	if err != nil {
		return nil, err
	}
	if gt := os.Getenv("GITHUB_TOKEN"); gt != "" {
		req.Header = make(http.Header)
		req.Header.Add("authorization", "token "+gt)
	}

	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: %s", reqURL, res.Status)
	}

	d := json.NewDecoder(res.Body)

	if err := d.Decode(&release); err != nil {
		return nil, err
	}

	return &release, nil
}

func (a *githubReleaseAsset) getAssetsByReleaseID(owner, repo string, releaseID int64) ([]Asset, error) {
	var assets []Asset

	reqURL := fmt.Sprintf("https://api.github.com/repos/%s/%s/releases/%d/assets", owner, repo, releaseID)

	req, err := http.NewRequest("GET", reqURL, nil)
	if err != nil {
		return nil, err
	}
	if gt := os.Getenv("GITHUB_TOKEN"); gt != "" {
		req.Header = make(http.Header)
		req.Header.Add("authorization", "token "+gt)
	}

	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: %s", reqURL, res.Status)
	}

	d := json.NewDecoder(res.Body)

	if err := d.Decode(&assets); err != nil {
		return nil, err
	}

	return assets, nil
}

func (a *githubReleaseAsset) getFile(dst string, owner, repo string, assetID int64) error {
	// Create all the parent directories if needed
	dir := filepath.Dir(dst)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("mkdir %s: %w", dir, err)
	}

	req, err := http.NewRequest("GET", fmt.Sprintf("https://api.github.com/repos/%s/%s/releases/assets/%d", owner, repo, assetID), nil)
	if err != nil {
		log.Fatal(err)
	}
	req.Header = make(http.Header)
	if gt := os.Getenv("GITHUB_TOKEN"); gt != "" {
		req.Header.Add("authorization", "token "+gt)
	}
	req.Header.Add("accept", "application/octet-stream")

	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()

	f, err := os.OpenFile(dst, os.O_RDWR|os.O_CREATE, os.FileMode(0666))
	if err != nil {
		return fmt.Errorf("open file %s: %w", dst, err)
	}
	defer f.Close()

	if _, err := io.Copy(f, res.Body); err != nil {
		return err
	}

	return nil
}

func (a *githubReleaseAsset) sign(path string) (string, error) {
	pass := os.Getenv("SIGNREL_PASSWORD")
	cmd := exec.Command("gpg", "--armor", "--detach-sign", "--pinentry-mode", "loopback", "--passphrase", pass, path)
	cap, err := cmd.CombinedOutput()
	if err != nil {
		fmt.Fprintf(os.Stderr, "gpg: %s", string(cap))
		return "", err
	}
	return path + ".asc", nil
}

func (a *githubReleaseAsset) upload(sig string, releaseID int64) error {
	assetName := filepath.Base(sig)
	url := fmt.Sprintf("https://uploads.github.com/repos/%s/%s/releases/%d/assets?name=%s", owner, repo, releaseID, assetName)
	f, err := os.Open(sig)
	if err != nil {
		return err
	}
	defer f.Close()

	size, err := f.Seek(0, 2)
	if err != nil {
		return err
	}

	_, err = f.Seek(0, 0)
	if err != nil {
		return err
	}

	req, err := http.NewRequest("POST", url, f)
	if err != nil {
		log.Fatal(err)
	}
	req.Header = make(http.Header)
	if gt := os.Getenv("GITHUB_TOKEN"); gt != "" {
		req.Header.Add("authorization", "token "+gt)
	}
	req.Header.Add("content-type", "application/octet-stream")

	req.ContentLength = size
	req.Header.Add("accept", "application/vnd.github.v3+json")

	// blob, _ := httputil.DumpRequestOut(req, true)
	// fmt.Printf("%s\n", blob)

	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()

	body, err := io.ReadAll(res.Body)
	if err != nil {
		return err
	}

	if res.StatusCode == 422 {
		fmt.Fprintf(os.Stdout, "%s has been already uploaded\n", sig)
		return nil
	}

	if res.StatusCode >= 300 {
		return fmt.Errorf("unexpected http status %d: %s", res.StatusCode, body)
	}

	fmt.Fprintf(os.Stdout, "Upload completed: %s\n", body)

	return err
}
