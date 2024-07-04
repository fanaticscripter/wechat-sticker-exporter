//go:build darwin

package sticker

import (
	"bytes"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"time"

	"github.com/antchfx/xmlquery"
	"github.com/avast/retry-go"
	log "github.com/sirupsen/logrus"
)

var _client *http.Client

type Sticker struct {
	ID  string
	URL string
	// EncryptedPath is the expected path of the encrypted sticker on disk. We
	// use this to determine Mtime.
	EncryptedPath string
	// DownloadedPath is the on disk path of the downloaded sticker. It is set
	// only after a successful call to DownloadTo().
	DownloadedPath string
	Mtime          time.Time
}

func init() {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DialContext = (&net.Dialer{Timeout: 5 * time.Second}).DialContext
	transport.TLSHandshakeTimeout = 5 * time.Second
	transport.ResponseHeaderTimeout = 5 * time.Second
	_client = &http.Client{
		Transport: transport,
		Timeout:   30 * time.Second,
	}
}

func ExtractStickerMetadata() (stickers []Sticker, err error) {
	// Look for ~/Library/Containers/com.tencent.xinWeChat/Data/Library/Application Support/com.tencent.xinWeChat/2.0b4.0.9/<alphanumeric>/Stickers/fav.archive.
	home, err := os.UserHomeDir()
	if err != nil {
		err = fmt.Errorf("failed to get user home dir: %w", err)
	}
	globPattern := filepath.Join(home, "Library/Containers/com.tencent.xinWeChat/Data/Library/Application Support/com.tencent.xinWeChat/*/*/Stickers/fav.archive")
	favArchives, _ := filepath.Glob(globPattern)
	if len(favArchives) == 0 {
		err = fmt.Errorf("failed to find fav.archive: no match for glob pattern %q", globPattern)
		return
	}
	var failures int
	for _, favArchive := range favArchives {
		stickersInArchive, extractErr := ExtractStickerMetadataFromFavArchive(favArchive)
		if extractErr != nil {
			log.Errorf("failed to extract stickers from %q: %s", favArchive, extractErr)
			failures++
			continue
		}
		stickers = append(stickers, stickersInArchive...)
	}
	if failures > 0 {
		err = fmt.Errorf("failed to extract stickers from %d/%d fav.archive file(s)", failures, len(favArchives))
	}
	return
}

func ExtractStickerMetadataFromFavArchive(favArchivePath string) (stickers []Sticker, err error) {
	xmlcontent, err := plistToXml(favArchivePath)
	if err != nil {
		return
	}
	doc, err := xmlquery.Parse(bytes.NewReader(xmlcontent))
	if err != nil {
		err = fmt.Errorf("failed to parse xml1 format of %q as XML: %s", favArchivePath, err)
		return
	}
	nodes := xmlquery.Find(doc, "/plist/dict/array/string")
	idpattern := regexp.MustCompile(`^[0-9a-f]+$`)
	urlpattern := regexp.MustCompile(`^https?://`)
	stickersDir := filepath.Join(filepath.Dir(favArchivePath), "Persistence")
	for i, node := range nodes {
		text := node.InnerText()
		if !urlpattern.MatchString(text) {
			continue
		}
		url := text
		if i == 0 {
			log.Warnf("found url %q at the beginning without an id before it, skipped", url)
			continue
		}
		id := nodes[i-1].InnerText()
		if !idpattern.MatchString(id) {
			log.Warnf("the string %q before %q isn't a valid alphanumeric id, skipped", id, url)
			continue
		}
		encryptedPath := filepath.Join(stickersDir, id)
		var mtime time.Time
		if fi, statErr := os.Stat(encryptedPath); statErr != nil {
			log.Warnf("failed to stat %q, cannot determine mtime: %s", encryptedPath, statErr)
		} else {
			mtime = fi.ModTime()
		}
		stickers = append(stickers, Sticker{
			ID:            id,
			URL:           url,
			EncryptedPath: encryptedPath,
			Mtime:         mtime,
		})
	}
	return
}

func (s *Sticker) DownloadTo(dir string) error {
	if s.ID == "" {
		return fmt.Errorf("Sticker.DownloadTo: Sticker.ID cannot be empty")
	}
	if s.URL == "" {
		return fmt.Errorf("Sticker.DownloadTo: Sticker.URL cannot be empty")
	}
	content, err := httpGetWithRetries(s.URL, 3)
	if err != nil {
		return fmt.Errorf("failed to download sticker %s to %q: %w", s.ID, dir, err)
	}
	mime := http.DetectContentType(content)
	var ext string
	switch mime {
	case "image/gif":
		ext = ".gif"
	case "image/jpeg":
		ext = ".jpg"
	case "image/png":
		ext = ".png"
	default:
		log.Warnf("sticker %s: unrecognized mime type %s, falling back to .png extension", s.ID, mime)
		ext = ".png"
	}
	dest := filepath.Join(dir, s.ID+ext)
	err = atomicWriteFile(dest, content, 0o644)
	if err != nil {
		return fmt.Errorf("failed to write downloaded sticker %s to %q: %w", s.ID, dest, err)
	}
	log.Infof("sticker %s: downloaded to %s", s.ID, dest)
	if s.Mtime.IsZero() {
		log.Warnf("sticker %s: cannot determine mtime, setting mtime to posix epoch 0", s.ID)
	}
	err = os.Chtimes(dest, s.Mtime, s.Mtime)
	if err != nil {
		log.Errorf("sticker %s: failed to set mtime to %s: %s", s.ID, s.Mtime, err)
	}
	s.DownloadedPath = dest
	return nil
}

func plistToXml(plistPath string) (xmlcontent []byte, err error) {
	cmd := exec.Command("plutil", "-convert", "xml1", "-o", "-", plistPath)
	xmlcontent, err = cmd.Output()
	if err != nil {
		err = fmt.Errorf("failed to convert plist to xml: plutil -convert xml1 -o - %q: %s", plistPath, err)
		return
	}
	return
}

func httpGetWithRetries(url string, retries uint) (content []byte, err error) {
	err = retry.Do(
		func() error {
			content, err = httpGet(url)
			if err != nil {
				log.Warn(err)
			}
			return err
		},
		retry.Attempts(retries),
		retry.Delay(3*time.Second),
		retry.DelayType(retry.FixedDelay),
	)
	return
}

func httpGet(url string) (content []byte, err error) {
	resp, err := _client.Get(url)
	if err != nil {
		err = fmt.Errorf("GET %s: %w", url, err)
		return
	}
	defer resp.Body.Close()
	code := resp.StatusCode
	if code != 200 {
		err = fmt.Errorf("GET %s: HTTP %d", url, code)
		if code >= 400 && code < 500 {
			err = retry.Unrecoverable(err)
		}
		return
	}
	content, err = io.ReadAll(resp.Body)
	if err != nil {
		err = fmt.Errorf("GET %s: failed to read response body: %w", url, err)
		return
	}
	return
}

func atomicWriteFile(name string, data []byte, perm os.FileMode) error {
	tmpfile, err := os.CreateTemp(filepath.Dir(name), filepath.Base(name)+".*")
	if err != nil {
		return fmt.Errorf("failed to create tmp file %q: %w", name+".*", err)
	}
	tmppath := tmpfile.Name()
	defer func() { _ = os.Remove(tmppath) }()
	_, err = tmpfile.Write(data)
	if err != nil {
		return fmt.Errorf("failed to write to tmp file %q: %w", tmppath, err)
	}
	if err := tmpfile.Chmod(perm); err != nil {
		return fmt.Errorf("failed to chmod tmp file %q to %s: %w", tmppath, perm, err)
	}
	if err := tmpfile.Close(); err != nil {
		return fmt.Errorf("failed to close tmp file %q: %w", tmppath, err)
	}
	if err := os.Rename(tmppath, name); err != nil {
		return fmt.Errorf("failed to rename tmp file %q to %q: %w", tmppath, name, err)
	}
	return nil
}
